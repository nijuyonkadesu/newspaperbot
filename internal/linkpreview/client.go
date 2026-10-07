// Package linkpreview reads optional website cards without changing post content.
package linkpreview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const (
	maxHTML  = 1 << 20
	maxImage = 1 << 20
	maxCache = 32
)

type Card struct {
	URL, Title, Description string
	Image                   []byte
	ImageFormat             string
}

type entry struct {
	card    Card
	expires time.Time
}

type Client struct {
	http  *http.Client
	mu    sync.Mutex
	cache map[string]entry
}

// New uses a bounded public-web transport by default. A supplied client allows
// deterministic local tests without exposing production previews to host services.
func New(client *http.Client) *Client {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = publicDial
		client = &http.Client{Transport: transport}
	}
	copy := *client
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || !webURL(req.URL) {
			return errors.New("invalid preview redirect")
		}
		return nil
	}
	return &Client{http: &copy, cache: map[string]entry{}}
}

// Lookup uses the first eligible URL. Missing metadata and network failures are
// normal cacheable misses; callers can always keep their original rich message.
func (c *Client) Lookup(ctx context.Context, markdown string) Card {
	raw := firstURL(markdown)
	if raw == "" || ctx.Err() != nil {
		return Card{}
	}
	c.mu.Lock()
	cached, ok := c.cache[raw]
	c.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.card
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	card := c.fetch(fetchCtx, raw)
	if ctx.Err() != nil {
		return Card{}
	}
	ttl := 30 * time.Minute
	if card.Title == "" {
		ttl = time.Minute
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= maxCache {
		oldest := raw
		for key, value := range c.cache {
			if previous, exists := c.cache[oldest]; !exists || value.expires.Before(previous.expires) {
				oldest = key
			}
		}
		delete(c.cache, oldest)
	}
	c.cache[raw] = entry{card: card, expires: time.Now().Add(ttl)}
	return card
}

// Cached never performs network I/O. Renderers can reuse a ready card without
// delaying the original Markdown while a cold URL is fetched in the background.
func (c *Client) Cached(markdown string) (Card, bool) {
	raw := firstURL(markdown)
	if raw == "" {
		return Card{}, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.cache[raw]
	return value.card, ok && time.Now().Before(value.expires)
}

// OmitImage avoids uploading a cached thumbnail again after Telegram rejects it.
func (c *Client) OmitImage(raw string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.cache[raw]; ok {
		cached.card.Image = nil
		c.cache[raw] = cached
	}
}

func (c *Client) request(ctx context.Context, raw string) (*http.Response, error) {
	u, err := url.Parse(raw)
	if err != nil || !webURL(u) {
		return nil, errors.New("invalid preview URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "newspaperbot (link preview)")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, errors.New("preview URL returned an unsuccessful status")
	}
	return resp, nil
}

func (c *Client) get(ctx context.Context, raw string, limit int64) ([]byte, error) {
	resp, err := c.request(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return data, errors.New("preview response is incomplete or too large")
	}
	return data, nil
}

func (c *Client) fetch(ctx context.Context, raw string) Card {
	var values map[string]string
	base, _ := url.Parse(raw)
	if endpoint := youtubeEndpoint(base); endpoint != "" {
		values = c.oembedMetadata(ctx, endpoint)
	} else if endpoint := redditEndpoint(base); endpoint != "" {
		values = c.oembedMetadata(ctx, endpoint)
		if author := values["og:description"]; author != "" {
			values["og:description"] = redditContext(base, author)
		}
	} else {
		original := base
		var final *url.URL
		values, final = c.pageMetadata(ctx, raw)
		if final != nil {
			base = final
		}
		if values == nil {
			values = map[string]string{}
		}
		endpoint := ""
		if !useful(values, base) {
			endpoint = twitterEndpoint(original)
		}
		if endpoint == "" && first(values, "og:title", "twitter:title") == "" && values["oembed"] != "" {
			if u, err := url.Parse(values["oembed"]); err == nil {
				endpoint = base.ResolveReference(u).String()
			}
		}
		if endpoint != "" {
			embed := c.oembedMetadata(ctx, endpoint)
			if useful(embed, base) {
				if !useful(values, base) {
					values = embed
				} else {
					for key, value := range embed {
						if value != "" && first(values, key) == "" {
							values[key] = value
						}
					}
				}
			}
		}
	}
	if !useful(values, base) {
		return Card{}
	}
	card := Card{URL: raw, Title: clean(first(values, "og:title", "twitter:title", "title"), 160),
		Description: clean(first(values, "og:description", "twitter:description", "description"), 300)}
	if card.Title == "" {
		return Card{}
	}
	imageURL, err := url.Parse(first(values, "og:image:secure_url", "og:image", "twitter:image", "twitter:image:src"))
	if err != nil || imageURL.String() == "" {
		return card
	}
	data, err := c.get(ctx, base.ResolveReference(imageURL).String(), maxImage)
	// GIF decoding needs only its first frame, even when the remaining animation
	// exceeds our download limit. Other formats require the complete response.
	if err == nil || bytes.HasPrefix(data, []byte("GIF8")) {
		card.Image, card.ImageFormat = photo(data)
	}
	return card
}

func (c *Client) pageMetadata(ctx context.Context, raw string) (map[string]string, *url.URL) {
	resp, err := c.request(ctx, raw)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		kind, params, err := mime.ParseMediaType(contentType)
		charset := strings.ToLower(params["charset"])
		if err != nil || kind != "text/html" && kind != "application/xhtml+xml" || charset != "" && charset != "utf-8" && charset != "us-ascii" {
			return nil, nil
		}
	}
	// Most pages need only the head; YouTube playlists put metadata just after it.
	// Both paths stop early and cap the bytes read, regardless of article size.
	reader := &io.LimitedReader{R: resp.Body, N: maxHTML + 1}
	host := strings.ToLower(resp.Request.URL.Hostname())
	values := metadata(reader, host == "youtube.com" || strings.HasSuffix(host, ".youtube.com"))
	if reader.N == 0 {
		return nil, nil
	}
	return values, resp.Request.URL
}

func metadata(reader io.Reader, scanBody bool) map[string]string {
	values := map[string]string{}
	z := html.NewTokenizer(reader)
	z.SetMaxBuf(maxHTML)
	inTitle, inBody := false, false
	for {
		tokenType := z.Next()
		if !utf8.Valid(z.Raw()) {
			return nil
		}
		switch tokenType {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				return nil
			}
			return values
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			if inBody && token.Data != "meta" && token.Data != "link" && first(values, "og:title", "twitter:title") != "" {
				return values
			}
			if token.Data == "body" {
				if !scanBody {
					return values
				}
				inBody = true
			}
			inTitle = token.Data == "title"
			if token.Data == "link" {
				kind, href := "", ""
				for _, attr := range token.Attr {
					switch attr.Key {
					case "type":
						kind = attr.Val
					case "href":
						href = attr.Val
					}
				}
				if kind == "application/json+oembed" {
					values["oembed"] = href
				}
			}
			if token.Data == "meta" {
				key, value := "", ""
				for _, attr := range token.Attr {
					switch attr.Key {
					case "property", "name":
						key = strings.ToLower(attr.Val)
					case "content":
						value = attr.Val
					}
				}
				if strings.TrimSpace(values[key]) == "" && key != "" {
					values[key] = value
				}
			}
		case html.EndTagToken:
			if z.Token().Data == "head" && !scanBody {
				return values
			}
			inTitle = false
		case html.TextToken:
			if inTitle {
				values["title"] += string(z.Text())
			}
		}
	}
}

func first(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func clean(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}

func webURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && len(u.String()) <= 4096
}

// Dial a checked address directly so redirects and DNS rebinding cannot turn a
// website preview into a request to the bot's localhost or private network.
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	var lastError error
	for _, ip := range ips {
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
			continue
		}
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastError = err
	}
	if lastError != nil {
		return nil, lastError
	}
	return nil, errors.New("preview URL has no public address")
}

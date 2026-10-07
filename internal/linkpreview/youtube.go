package linkpreview

import (
	"net/url"
	"strings"
)

// YouTube puts metadata after large scripts on its watch page. Its public
// oEmbed endpoint returns video metadata directly, without an API key or HTML.
func youtubeEndpoint(u *url.URL) string {
	id := ""
	switch strings.ToLower(u.Hostname()) {
	case "youtu.be", "www.youtu.be":
		id = strings.TrimPrefix(u.Path, "/")
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com", "www.youtube-nocookie.com":
		if u.Path == "/watch" {
			id = u.Query().Get("v")
		} else {
			for _, prefix := range []string{"/shorts/", "/embed/", "/live/"} {
				if strings.HasPrefix(u.Path, prefix) {
					id = strings.TrimPrefix(u.Path, prefix)
					break
				}
			}
		}
	}
	if len(id) != 11 {
		return ""
	}
	for _, r := range id {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return ""
		}
	}
	return "https://www.youtube.com/oembed?format=json&url=" + url.QueryEscape("https://www.youtube.com/watch?v="+id)
}

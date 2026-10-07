package linkpreview

import (
	"net/url"
	"strings"
)

// A brand label or access/login screen is not a preview of the linked content.
func useful(values map[string]string, u *url.URL) bool {
	title := strings.ToLower(clean(first(values, "og:title", "twitter:title", "title"), 160))
	if title == "" || u == nil {
		return false
	}
	switch strings.TrimRight(title, ".… ") {
	case "access denied", "just a moment", "please wait", "attention required! | cloudflare", "welcome to reddit", "403 forbidden", "404 not found":
		return false
	}
	description := strings.ToLower(clean(first(values, "og:description", "twitter:description", "description"), 300))
	if description != "" && description != title {
		return true
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	brand := strings.Split(host, ".")[0]
	return title != host && title != brand && title != strings.ToLower(values["og:site_name"]) && title != "x on x" && title != "reddit - the heart of the internet"
}

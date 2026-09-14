package provider

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// FetchIcon retrieves public site artwork without forwarding provider credentials.
// Store the returned data URL in Provider.Icon so later rendering works offline.
func FetchIcon(ctx context.Context, client *http.Client, address string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	root, err := url.Parse(strings.TrimSpace(address))
	if err != nil || root.Host == "" || (root.Scheme != "http" && root.Scheme != "https") || root.User != nil {
		return "", fmt.Errorf("请输入有效的 HTTP(S) Base URL")
	}
	root.Path, root.RawPath, root.RawQuery, root.Fragment = "/", "", "", ""
	fetch := func(target *url.URL) ([]byte, *url.URL, error) {
		if target.User != nil || (target.Scheme != "http" && target.Scheme != "https") {
			return nil, nil, fmt.Errorf("unsupported icon URL")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, nil, err
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("HTTP %d", res.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 {
			return nil, nil, fmt.Errorf("icon response unavailable or too large")
		}
		return body, res.Request.URL, nil
	}
	candidates := []*url.URL{}
	if body, pageURL, err := fetch(root); err == nil {
		tokenizer := html.NewTokenizer(strings.NewReader(string(body)))
		for {
			tt := tokenizer.Next()
			if tt == html.ErrorToken {
				break
			}
			if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
				continue
			}
			token := tokenizer.Token()
			if token.Data != "link" {
				continue
			}
			var rel, href string
			for _, attr := range token.Attr {
				if attr.Key == "rel" {
					rel = strings.ToLower(attr.Val)
				}
				if attr.Key == "href" {
					href = attr.Val
				}
			}
			isIcon := false
			for _, value := range strings.Fields(rel) {
				if value == "icon" || value == "apple-touch-icon" {
					isIcon = true
				}
			}
			if isIcon && href != "" {
				if u, err := url.Parse(href); err == nil {
					candidates = append(candidates, pageURL.ResolveReference(u))
				}
			}
			if len(candidates) >= 5 {
				break
			}
		}
	}
	candidates = append(candidates, root.ResolveReference(&url.URL{Path: "/favicon.ico"}))
	for _, candidate := range candidates {
		body, _, err := fetch(candidate)
		if err != nil {
			continue
		}
		mime := http.DetectContentType(body)
		if !strings.HasPrefix(mime, "image/") {
			var svg struct{ XMLName xml.Name }
			if xml.Unmarshal(body, &svg) == nil && svg.XMLName.Local == "svg" {
				mime = "image/svg+xml"
			}
		}
		if !strings.HasPrefix(mime, "image/") {
			continue
		}
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body), nil
	}
	return "", fmt.Errorf("未找到可用的 favicon，将使用默认图标")
}

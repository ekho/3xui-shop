package vpn

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func httpsURL(raw string, trailingSlash bool) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || raw != strings.TrimSpace(raw) || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\\\r\n\t") {
		return "", ErrPanel
	}
	if strings.Contains(raw, "#") {
		return "", ErrPanel
	}
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrPanel
		}
		if n != 443 {
			host = net.JoinHostPort(host, strconv.Itoa(n))
		} else if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", ErrPanel
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if trailingSlash {
		u.Path += "/"
	}
	u.RawPath = ""
	return u.String(), nil
}

func panelSubscriptionBase(host string, raw json.RawMessage) (string, error) {
	var setting struct {
		Enabled bool   `json:"subEnable"`
		URI     string `json:"subURI"`
		Domain  string `json:"subDomain"`
		Port    int    `json:"subPort"`
		Path    string `json:"subPath"`
		Key     string `json:"subKeyFile"`
		Cert    string `json:"subCertFile"`
	}
	if json.Unmarshal(raw, &setting) != nil || !setting.Enabled {
		return "", ErrPanel
	}
	if setting.URI != "" {
		return httpsURL(setting.URI, true)
	}
	if setting.Key == "" || setting.Cert == "" || setting.Port < 1 || setting.Port > 65535 || !strings.HasPrefix(setting.Path, "/") || strings.ContainsAny(setting.Path, "?#") {
		return "", ErrPanel
	}
	panel, err := url.Parse(host)
	if err != nil || panel.Hostname() == "" {
		return "", ErrPanel
	}
	domain := setting.Domain
	if domain == "" {
		domain = panel.Hostname()
	}
	domain = strings.TrimPrefix(strings.TrimSuffix(domain, "]"), "[")
	if strings.ContainsAny(domain, "/?#@\\ ") || strings.Contains(domain, ":") && net.ParseIP(domain) == nil {
		return "", ErrPanel
	}
	return httpsURL("https://"+net.JoinHostPort(domain, strconv.Itoa(setting.Port))+setting.Path, true)
}

func (p *PanelClient) subscriptionBase(ctx context.Context) (string, error) {
	if p.auth(ctx) != nil {
		return "", ErrPanel
	}
	out, err := p.call(ctx, "POST", "panel/api/setting/all", nil)
	if err != nil || !*out.Success {
		return "", ErrPanel
	}
	return panelSubscriptionBase(p.cfg.PanelURL, out.Obj)
}

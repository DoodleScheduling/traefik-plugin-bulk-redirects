package traefik_plugin_bulk_redirects

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type Config struct {
	Redirects []Redirect `json:"redirects,omitempty"`
}

type Redirect struct {
	SourceHost          string `json:"sourceHost,omitempty"`
	SourcePath          string `json:"sourcePath,omitempty"`
	TargetURL           string `json:"targetURL,omitempty"`
	StatusCode          int    `json:"statusCode,omitempty"`
	PreserveQueryString string `json:"preserveQueryString,omitempty"`
	SubpathMatching     string `json:"subpathMatching,omitempty"`
}

func CreateConfig() *Config {
	return &Config{
		Redirects: []Redirect{},
	}
}

type BulkRedirects struct {
	next            http.Handler
	name            string
	exactRedirects  map[string]Redirect
	prefixRedirects []Redirect
}

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	_ = ctx
	exactRedirects := make(map[string]Redirect, len(config.Redirects))
	var prefixRedirects []Redirect

	for _, redirect := range config.Redirects {
		if redirect.StatusCode == 0 {
			redirect.StatusCode = http.StatusMovedPermanently
		}
		if !isValidRedirectStatusCode(redirect.StatusCode) {
			return nil, fmt.Errorf("invalid StatusCode %d for %s%s", redirect.StatusCode, redirect.SourceHost, redirect.SourcePath)
		}
		if redirect.SourceHost == "" {
			return nil, fmt.Errorf("sourceHost is required")
		}
		if strings.Contains(redirect.SourceHost, "://") || strings.Contains(redirect.SourceHost, "/") {
			return nil, fmt.Errorf("sourceHost must be a hostname only, got %q", redirect.SourceHost)
		}
		if redirect.SourcePath == "" {
			return nil, fmt.Errorf("sourcePath is required for host %s", redirect.SourceHost)
		}
		if !strings.HasPrefix(redirect.SourcePath, "/") {
			return nil, fmt.Errorf("sourcePath must start with / for %s%s", redirect.SourceHost, redirect.SourcePath)
		}
		if redirect.TargetURL == "" {
			return nil, fmt.Errorf("targetURL is required for %s%s", redirect.SourceHost, redirect.SourcePath)
		}
		if !isValidEnabledDisabledValue(redirect.PreserveQueryString) {
			return nil, fmt.Errorf("invalid preserveQueryString %q for %s%s", redirect.PreserveQueryString, redirect.SourceHost, redirect.SourcePath)
		}
		if !isValidEnabledDisabledValue(redirect.SubpathMatching) {
			return nil, fmt.Errorf("invalid subpathMatching %q for %s%s", redirect.SubpathMatching, redirect.SourceHost, redirect.SourcePath)
		}

		redirect.SourceHost = normalizeHost(redirect.SourceHost)

		if strings.EqualFold(redirect.SubpathMatching, "enabled") {
			prefixRedirects = append(prefixRedirects, redirect)
			continue
		}
		key := redirect.SourceHost + redirect.SourcePath
		exactRedirects[key] = redirect
	}

	return &BulkRedirects{
		next:            next,
		name:            name,
		exactRedirects:  exactRedirects,
		prefixRedirects: prefixRedirects,
	}, nil
}

func (bulkRedirects *BulkRedirects) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	path := req.URL.Path
	key := host + path

	if redirect, found := bulkRedirects.exactRedirects[key]; found {
		bulkRedirects.redirect(rw, req, redirect, path)
		return
	}

	for _, redirect := range bulkRedirects.prefixRedirects {
		if redirect.SourceHost != host {
			continue
		}

		if !isSubpathMatch(path, redirect.SourcePath) {
			continue
		}

		bulkRedirects.redirect(rw, req, redirect, path)
		return
	}

	bulkRedirects.next.ServeHTTP(rw, req)
}

func (bulkRedirects *BulkRedirects) redirect(rw http.ResponseWriter, req *http.Request, redirect Redirect, requestPath string) {
	target := redirect.TargetURL

	if strings.EqualFold(redirect.SubpathMatching, "enabled") {
		suffix := strings.TrimPrefix(requestPath, redirect.SourcePath)
		if suffix != "" && suffix != "/" {
			target = strings.TrimRight(target, "/") + "/" + strings.TrimLeft(suffix, "/")
		}
	}

	if strings.EqualFold(redirect.PreserveQueryString, "enabled") && req.URL.RawQuery != "" {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}

		target += separator + req.URL.RawQuery
	}

	http.Redirect(rw, req, target, redirect.StatusCode)
}

func normalizeHost(host string) string {
	host = strings.ToLower(host)

	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}

	return host
}

func isValidRedirectStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently, // 301
		http.StatusFound,             //302
		http.StatusSeeOther,          //303
		http.StatusTemporaryRedirect, //307
		http.StatusPermanentRedirect: //308
		return true
	default:
		return false
	}
}

func isSubpathMatch(path, sourcePath string) bool {
	if path == sourcePath {
		return true
	}

	if strings.HasSuffix(sourcePath, "/") {
		return strings.HasPrefix(path, sourcePath)
	}

	return strings.HasPrefix(path, sourcePath+"/")
}

func isValidEnabledDisabledValue(value string) bool {
	return value == "" || strings.EqualFold(value, "enabled") || strings.EqualFold(value, "disabled")
}

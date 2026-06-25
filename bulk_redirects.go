package traefik_bulk_redirects

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type Config struct {
	Redirects []Redirect `json:"redirects,omitempty"`
}

type Redirect struct {
	SourceURL           string           `json:"sourceURL,omitempty"`
	TargetURL           string           `json:"targetURL,omitempty"`
	StatusCode          int              `json:"statusCode,omitempty"`
	PreserveQueryString string           `json:"preserveQueryString,omitempty"`
	SubpathMatching     string           `json:"subpathMatching,omitempty"`
	Dynamic             *DynamicRedirect `json:"dynamic,omitempty"`
}

type DynamicRedirect struct {
	Enabled             bool              `json:"enabled,omitempty"`
	StatusCode          int               `json:"statusCode,omitempty"`
	PreserveQueryString string            `json:"preserveQueryString,omitempty"`
	AuthenticatedCookie string            `json:"authenticatedCookie,omitempty"`
	AuthenticatedTarget string            `json:"authenticatedTarget,omitempty"`
	LocaleCookie        string            `json:"localeCookie,omitempty"`
	DefaultTarget       string            `json:"defaultTarget,omitempty"`
	LocaleTargets       map[string]string `json:"localeTargets,omitempty"`
}

type Target struct {
	URL                 string
	StatusCode          int
	PreserveQueryString string
}

type PrefixRedirect struct {
	SourcePath string
	Target     Target
}

type RuntimeDynamicRedirect struct {
	StatusCode          int
	PreserveQueryString string
	AuthenticatedCookie string
	AuthenticatedTarget string
	LocaleCookie        string
	DefaultTarget       string
	LocaleTargets       map[string]string
}

func CreateConfig() *Config {
	return &Config{
		Redirects: []Redirect{},
	}
}

type BulkRedirects struct {
	next             http.Handler
	name             string
	exactRedirects   map[string]Target
	prefixRedirects  map[string]PrefixRedirect
	dynamicRedirects map[string]RuntimeDynamicRedirect
}

func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	_ = ctx

	exactRedirects := make(map[string]Target, len(config.Redirects))
	prefixRedirects := make(map[string]PrefixRedirect)
	dynamicRedirects := make(map[string]RuntimeDynamicRedirect)

	for _, redirect := range config.Redirects {
		if redirect.SourceURL == "" {
			return nil, fmt.Errorf("sourceURL is required")
		}

		sourceHost, sourcePath, err := parseSourceURL(redirect.SourceURL)
		if err != nil {
			return nil, err
		}

		key := buildKey(sourceHost, sourcePath)

		if isDynamicRedirect(redirect) {
			if err := validateDynamicRedirect(redirect.SourceURL, redirect); err != nil {
				return nil, err
			}

			dynamicRedirects[key] = RuntimeDynamicRedirect{
				StatusCode:          redirect.Dynamic.StatusCode,
				PreserveQueryString: redirect.Dynamic.PreserveQueryString,
				AuthenticatedCookie: redirect.Dynamic.AuthenticatedCookie,
				AuthenticatedTarget: redirect.Dynamic.AuthenticatedTarget,
				LocaleCookie:        redirect.Dynamic.LocaleCookie,
				DefaultTarget:       redirect.Dynamic.DefaultTarget,
				LocaleTargets:       normalizeLocaleTargets(redirect.Dynamic.LocaleTargets),
			}

			continue
		}

		if err := validateStaticRedirect(redirect.SourceURL, &redirect); err != nil {
			return nil, err
		}

		target := Target{
			URL:                 redirect.TargetURL,
			StatusCode:          redirect.StatusCode,
			PreserveQueryString: redirect.PreserveQueryString,
		}

		if strings.EqualFold(redirect.SubpathMatching, "enabled") {
			prefixRedirects[key] = PrefixRedirect{
				SourcePath: sourcePath,
				Target:     target,
			}

			continue
		}

		exactRedirects[key] = target
	}

	return &BulkRedirects{
		next:             next,
		name:             name,
		exactRedirects:   exactRedirects,
		prefixRedirects:  prefixRedirects,
		dynamicRedirects: dynamicRedirects,
	}, nil
}

func (bulkRedirects *BulkRedirects) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}

	key := buildKey(host, path)

	if target, found := bulkRedirects.exactRedirects[key]; found {
		redirect(rw, req, target, "")
		return
	}

	if dynamicRedirect, found := bulkRedirects.dynamicRedirects[key]; found {
		if target, found := resolveDynamicRedirect(req, dynamicRedirect); found {
			redirect(rw, req, target, "")
			return
		}
	}

	if prefixRedirect, found := bulkRedirects.findPrefixRedirect(host, path); found {
		suffix := strings.TrimPrefix(path, prefixRedirect.SourcePath)
		redirect(rw, req, prefixRedirect.Target, suffix)
		return
	}

	bulkRedirects.next.ServeHTTP(rw, req)
}

func (bulkRedirects *BulkRedirects) findPrefixRedirect(host, path string) (PrefixRedirect, bool) {
	currentPath := path

	for {
		if prefixRedirect, found := bulkRedirects.findPrefixCandidate(host, path, currentPath); found {
			return prefixRedirect, true
		}

		if currentPath == "/" {
			break
		}

		trimmed := strings.TrimRight(currentPath, "/")
		lastSlash := strings.LastIndex(trimmed, "/")
		if lastSlash <= 0 {
			currentPath = "/"
			continue
		}

		currentPath = trimmed[:lastSlash+1]
	}

	return PrefixRedirect{}, false
}

func (bulkRedirects *BulkRedirects) findPrefixCandidate(host, path, candidate string) (PrefixRedirect, bool) {
	if prefixRedirect, found := bulkRedirects.prefixRedirects[buildKey(host, candidate)]; found {
		if isSubpathMatch(path, prefixRedirect.SourcePath) {
			return prefixRedirect, true
		}
	}

	if candidate == "/" {
		return PrefixRedirect{}, false
	}

	alternative := toggleTrailingSlash(candidate)

	if prefixRedirect, found := bulkRedirects.prefixRedirects[buildKey(host, alternative)]; found {
		if isSubpathMatch(path, prefixRedirect.SourcePath) {
			return prefixRedirect, true
		}
	}

	return PrefixRedirect{}, false
}

func toggleTrailingSlash(path string) string {
	if strings.HasSuffix(path, "/") {
		return strings.TrimRight(path, "/")
	}

	return path + "/"
}

func redirect(rw http.ResponseWriter, req *http.Request, target Target, suffix string) {
	targetURL := target.URL

	if suffix != "" && suffix != "/" {
		targetURL = strings.TrimRight(targetURL, "/") + "/" + strings.TrimLeft(suffix, "/")
	}

	if strings.EqualFold(target.PreserveQueryString, "enabled") && req.URL.RawQuery != "" {
		separator := "?"
		if strings.Contains(targetURL, "?") {
			separator = "&"
		}

		targetURL += separator + req.URL.RawQuery
	}

	http.Redirect(rw, req, targetURL, target.StatusCode)
}

func resolveDynamicRedirect(req *http.Request, dynamicRedirect RuntimeDynamicRedirect) (Target, bool) {
	if dynamicRedirect.AuthenticatedCookie != "" && dynamicRedirect.AuthenticatedTarget != "" {
		if cookie, err := req.Cookie(dynamicRedirect.AuthenticatedCookie); err == nil && cookie.Value != "" {
			return Target{
				URL:                 dynamicRedirect.AuthenticatedTarget,
				StatusCode:          dynamicRedirect.StatusCode,
				PreserveQueryString: dynamicRedirect.PreserveQueryString,
			}, true
		}
	}

	if dynamicRedirect.LocaleCookie != "" {
		if cookie, err := req.Cookie(dynamicRedirect.LocaleCookie); err == nil {
			locale := strings.ToLower(cookie.Value)

			if targetURL, found := dynamicRedirect.LocaleTargets[locale]; found {
				return Target{
					URL:                 targetURL,
					StatusCode:          dynamicRedirect.StatusCode,
					PreserveQueryString: dynamicRedirect.PreserveQueryString,
				}, true
			}
		}
	}

	if locale, found := findLocaleFromAcceptLanguage(req.Header.Get("Accept-Language"), dynamicRedirect.LocaleTargets); found {
		return Target{
			URL:                 dynamicRedirect.LocaleTargets[locale],
			StatusCode:          dynamicRedirect.StatusCode,
			PreserveQueryString: dynamicRedirect.PreserveQueryString,
		}, true
	}

	if dynamicRedirect.DefaultTarget != "" {
		return Target{
			URL:                 dynamicRedirect.DefaultTarget,
			StatusCode:          dynamicRedirect.StatusCode,
			PreserveQueryString: dynamicRedirect.PreserveQueryString,
		}, true
	}

	return Target{}, false
}

func parseSourceURL(sourceURL string) (string, string, error) {
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid sourceURL %q: %w", sourceURL, err)
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("sourceURL must be absolute, got %q", sourceURL)
	}

	if parsed.RawQuery != "" {
		return "", "", fmt.Errorf("sourceURL must not contain query string, got %q", sourceURL)
	}

	if parsed.Fragment != "" {
		return "", "", fmt.Errorf("sourceURL must not contain fragment, got %q", sourceURL)
	}

	host := normalizeHost(parsed.Host)
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}

	return host, path, nil
}

func validateTargetURL(targetURL string) error {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return err
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("targetURL must be absolute")
	}

	return nil
}

func validateStaticRedirect(sourceURL string, redirect *Redirect) error {
	if redirect.StatusCode == 0 {
		redirect.StatusCode = http.StatusMovedPermanently
	}

	if redirect.TargetURL == "" {
		return fmt.Errorf("targetURL is required for %s", sourceURL)
	}

	if err := validateTargetURL(redirect.TargetURL); err != nil {
		return fmt.Errorf("invalid targetURL %q for %s: %w", redirect.TargetURL, sourceURL, err)
	}

	if !isValidRedirectStatusCode(redirect.StatusCode) {
		return fmt.Errorf("invalid statusCode %d for %s", redirect.StatusCode, sourceURL)
	}

	if !isValidEnabledDisabledValue(redirect.PreserveQueryString) {
		return fmt.Errorf("invalid preserveQueryString %q for %s", redirect.PreserveQueryString, sourceURL)
	}

	if !isValidEnabledDisabledValue(redirect.SubpathMatching) {
		return fmt.Errorf("invalid subpathMatching %q for %s", redirect.SubpathMatching, sourceURL)
	}

	return nil
}

func validateDynamicRedirect(sourceURL string, redirect Redirect) error {
	dynamic := redirect.Dynamic
	if dynamic == nil || !dynamic.Enabled {
		return nil
	}

	if redirect.TargetURL != "" {
		return fmt.Errorf("targetURL must not be set when dynamic.enabled=true for %s", sourceURL)
	}

	if strings.EqualFold(redirect.SubpathMatching, "enabled") {
		return fmt.Errorf("subpathMatching is not supported when dynamic.enabled=true for %s", sourceURL)
	}

	if dynamic.StatusCode == 0 {
		dynamic.StatusCode = http.StatusFound
	}

	if !isValidRedirectStatusCode(dynamic.StatusCode) {
		return fmt.Errorf("invalid dynamic.statusCode %d for %s", dynamic.StatusCode, sourceURL)
	}

	if dynamic.PreserveQueryString == "" {
		dynamic.PreserveQueryString = "enabled"
	}

	if !isValidEnabledDisabledValue(dynamic.PreserveQueryString) {
		return fmt.Errorf("invalid dynamic.preserveQueryString %q for %s", dynamic.PreserveQueryString, sourceURL)
	}

	if dynamic.DefaultTarget == "" {
		return fmt.Errorf("dynamic.defaultTarget is required for %s", sourceURL)
	}

	if err := validateTargetURL(dynamic.DefaultTarget); err != nil {
		return fmt.Errorf("invalid dynamic.defaultTarget %q for %s: %w", dynamic.DefaultTarget, sourceURL, err)
	}

	if dynamic.AuthenticatedTarget != "" {
		if dynamic.AuthenticatedCookie == "" {
			return fmt.Errorf("dynamic.authenticatedCookie is required when dynamic.authenticatedTarget is set for %s", sourceURL)
		}

		if err := validateTargetURL(dynamic.AuthenticatedTarget); err != nil {
			return fmt.Errorf("invalid dynamic.authenticatedTarget %q for %s: %w", dynamic.AuthenticatedTarget, sourceURL, err)
		}
	}

	if dynamic.LocaleCookie != "" && len(dynamic.LocaleTargets) == 0 {
		return fmt.Errorf("dynamic.localeTargets is required when dynamic.localeCookie is set for %s", sourceURL)
	}

	for locale, targetURL := range dynamic.LocaleTargets {
		if locale == "" {
			return fmt.Errorf("dynamic.localeTargets contains empty locale for %s", sourceURL)
		}

		if err := validateTargetURL(targetURL); err != nil {
			return fmt.Errorf("invalid dynamic.localeTargets[%q]=%q for %s: %w", locale, targetURL, sourceURL, err)
		}
	}

	return nil
}

func isDynamicRedirect(redirect Redirect) bool {
	return redirect.Dynamic != nil && redirect.Dynamic.Enabled
}

func findLocaleFromAcceptLanguage(header string, supported map[string]string) (string, bool) {
	languages := strings.Split(header, ",")

	for _, language := range languages {
		language = strings.TrimSpace(strings.ToLower(language))
		language = strings.Split(language, ";")[0]

		if language == "" {
			continue
		}

		baseLanguage := strings.Split(language, "-")[0]

		if _, found := supported[baseLanguage]; found {
			return baseLanguage, true
		}
	}

	return "", false
}

func normalizeLocaleTargets(localeTargets map[string]string) map[string]string {
	normalized := make(map[string]string, len(localeTargets))

	for locale, targetURL := range localeTargets {
		normalized[strings.ToLower(locale)] = targetURL
	}

	return normalized
}

func normalizeHost(host string) string {
	host = strings.ToLower(host)

	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}

	return host
}

func buildKey(host, path string) string {
	return host + "\x00" + path
}

func isValidRedirectStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently, // 301
		http.StatusFound,             // 302
		http.StatusSeeOther,          // 303
		http.StatusTemporaryRedirect, // 307
		http.StatusPermanentRedirect: // 308
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

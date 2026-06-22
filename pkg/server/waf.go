package server

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	// SQL Injection patterns
	sqlPattern = regexp.MustCompile(`(?i)(union\s+select|select\s+.*from|insert\s+into|update\s+.*set|delete\s+from|'\s*or\s*1\s*=\s*1|--\s*$)`)
	// Cross-Site Scripting patterns
	xssPattern = regexp.MustCompile(`(?i)(<script|javascript:|onload\s*=|\bonerror\s*=|<iframe|<svg|<img\s+src\s*=.*onerror)`)
	// Path Traversal / Local File Inclusion patterns
	pathTraversalPattern = regexp.MustCompile(`(?i)(\.\./|\.\.\\|etc[/\\]passwd|windows[\\\\/]win\.ini|boot\.ini)`)
	// Shell/Command Injection patterns
	cmdInjectionPattern = regexp.MustCompile(`(?i)(;|\&\&|\|\|)\s*(whoami|id|uname|cat|ls|ping|curl|wget)\b`)
)

// IsMaliciousRequest scans the request parameters, headers, and body for common exploit signatures.
// Returns true, rule category, and description if a vulnerability is detected.
func IsMaliciousRequest(r *http.Request) (bool, string, string) {
	// 1. Scan URL Path
	if pathTraversalPattern.MatchString(r.URL.Path) {
		return true, "Path Traversal", "Path Traversal / Local File Inclusion detected in URL path"
	}
	if sqlPattern.MatchString(r.URL.Path) {
		return true, "SQL Injection", "SQL Injection payload detected in URL path"
	}
	if xssPattern.MatchString(r.URL.Path) {
		return true, "Cross-Site Scripting", "Cross-Site Scripting payload detected in URL path"
	}

	// 2. Scan Query parameters
	for key, values := range r.URL.Query() {
		for _, val := range values {
			unescaped, err := url.QueryUnescape(val)
			if err != nil {
				unescaped = val
			}
			if pathTraversalPattern.MatchString(unescaped) {
				return true, "Path Traversal", "Path Traversal detected in query parameter: " + key
			}
			if sqlPattern.MatchString(unescaped) {
				return true, "SQL Injection", "SQL Injection payload detected in query parameter: " + key
			}
			if xssPattern.MatchString(unescaped) {
				return true, "Cross-Site Scripting", "Cross-Site Scripting payload detected in query parameter: " + key
			}
			if cmdInjectionPattern.MatchString(unescaped) {
				return true, "Command Injection", "Command Injection payload detected in query parameter: " + key
			}
		}
	}

	// 3. Scan Headers (User-Agent, Cookie)
	if sqlPattern.MatchString(r.UserAgent()) {
		return true, "SQL Injection", "SQL Injection payload detected in User-Agent header"
	}
	if xssPattern.MatchString(r.UserAgent()) {
		return true, "Cross-Site Scripting", "Cross-Site Scripting payload detected in User-Agent header"
	}
	if cmdInjectionPattern.MatchString(r.UserAgent()) {
		return true, "Command Injection", "Command Injection payload detected in User-Agent header"
	}

	for _, cookie := range r.Cookies() {
		if sqlPattern.MatchString(cookie.Value) {
			return true, "SQL Injection", "SQL Injection payload detected in Cookie: " + cookie.Name
		}
		if xssPattern.MatchString(cookie.Value) {
			return true, "Cross-Site Scripting", "Cross-Site Scripting payload detected in Cookie: " + cookie.Name
		}
	}

	// 4. Scan Request Body (limit to text-like media types and max 8KB to avoid overhead/DOS)
	contentType := r.Header.Get("Content-Type")
	isText := contentType == "" ||
		strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(contentType, "application/json") ||
		strings.HasPrefix(contentType, "application/xml") ||
		strings.HasPrefix(contentType, "text/")

	if isText && r.Body != nil && r.Body != http.NoBody {
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 8192))
		if err == nil {
			// Restore request body reader so the actual reverse proxy can read it downstream
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			bodyStr := string(bodyBytes)
			if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
				if unescaped, err := url.QueryUnescape(bodyStr); err == nil {
					bodyStr = unescaped
				}
			}

			if pathTraversalPattern.MatchString(bodyStr) {
				return true, "Path Traversal", "Path Traversal detected in request body"
			}
			if sqlPattern.MatchString(bodyStr) {
				return true, "SQL Injection", "SQL Injection payload detected in request body"
			}
			if xssPattern.MatchString(bodyStr) {
				return true, "Cross-Site Scripting", "Cross-Site Scripting payload detected in request body"
			}
			if cmdInjectionPattern.MatchString(bodyStr) {
				return true, "Command Injection", "Command Injection payload detected in request body"
			}
		}
	}

	return false, "", ""
}

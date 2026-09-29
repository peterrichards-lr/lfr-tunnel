package server

import (
	"embed"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed i18n/*
var i18nFS embed.FS

// parseProperties loads standard Java-style property key/value pairs.
func parseProperties(content string) map[string]string {
	props := make(map[string]string)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Ignore empty lines and comments (starting with # or !)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx == -1 {
			idx = strings.Index(line, ":")
		}
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		props[key] = val
	}
	return props
}

// initI18n loads dynamic translation property bundles into server memory.
func (s *Server) initI18n() error {
	s.translations = make(map[string]map[string]string)
	locales := []string{"en", "es", "fr", "de", "pt", "ko", "ja", "zh", "ro", "ar"}

	// Local filesystem override directory
	externalDir := "/etc/lfr-tunneld/i18n"

	for _, locale := range locales {
		var content string
		loadedExternal := false

		// Determine property filename
		filename := "Language"
		if locale != "en" {
			filename = fmt.Sprintf("Language_%s", locale)
		}
		filename = filename + ".properties"

		// 1. Try loading from external directory first (Runtime customization!)
		extPath := filepath.Join(externalDir, filename)
		if _, err := os.Stat(extPath); err == nil {
			data, err := os.ReadFile(extPath)
			if err == nil {
				content = string(data)
				loadedExternal = true
				slog.Info(fmt.Sprintf("[i18n] Loaded runtime custom properties override for locale %q: %s", locale, extPath))
			}
		}

		// 2. Fall back to Go-embedded asset second
		if !loadedExternal {
			data, err := i18nFS.ReadFile(fmt.Sprintf("i18n/%s", filename))
			if err != nil {
				slog.Info(fmt.Sprintf("[i18n] Warning: failed to load embedded properties for locale %q: %v", locale, err))
				continue
			}
			content = string(data)
		}

		// Parse the properties format
		s.translations[locale] = parseProperties(content)
	}

	slog.Info(fmt.Sprintf("[i18n] Successfully initialized dynamic i18n engine with %d locales.", len(s.translations)))
	return nil
}

// GetTranslation retrieves a localized string by key with automatic English fallback.
func (s *Server) GetTranslation(lang, key string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if len(lang) > 2 {
		lang = lang[:2] // Normalize e.g. "en-US" -> "en"
	}

	// 1. Attempt target language
	if bundle, ok := s.translations[lang]; ok {
		if val, exists := bundle[key]; exists && val != "" {
			return val
		}
	}

	// 2. Fallback to English
	if bundle, ok := s.translations["en"]; ok {
		if val, exists := bundle[key]; exists && val != "" {
			return val
		}
	}

	// 3. Absolute Fallback: return the key itself
	return key
}

// ResolveLocale parses incoming HTTP request headers or query parameters to extract the best matching locale.
func (s *Server) ResolveLocale(r *http.Request) string {
	supported := map[string]bool{
		"en": true, "es": true, "fr": true, "de": true, "pt": true, "ko": true, "ja": true, "zh": true, "ro": true, "ar": true,
	}

	// 1. Check explicit query parameter first (e.g. ?lang=ro)
	langQuery := r.URL.Query().Get("lang")
	if langQuery != "" {
		langQuery = strings.ToLower(strings.TrimSpace(langQuery))
		if len(langQuery) > 2 {
			langQuery = langQuery[:2]
		}
		if supported[langQuery] {
			return langQuery
		}
	}

	// 2. Check Accept-Language header
	acceptLang := r.Header.Get("Accept-Language")
	if acceptLang == "" {
		return "en"
	}

	// Simple parser: e.g. "fr-CH, fr;q=0.9, en;q=0.8"
	parts := strings.Split(acceptLang, ",")

	for _, part := range parts {
		subparts := strings.Split(strings.TrimSpace(part), ";")
		lang := strings.ToLower(strings.TrimSpace(subparts[0]))
		if len(lang) > 2 {
			lang = lang[:2]
		}
		if supported[lang] {
			return lang
		}
	}

	return "en"
}

// handleGetI18n serves the parsed JSON translation bundle for the requested locale.
func (s *Server) handleGetI18n(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = s.ResolveLocale(r)
	}
	if len(lang) > 2 {
		lang = lang[:2]
	}

	bundle, ok := s.translations[lang]
	if !ok {
		// Fallback to English
		bundle = s.translations["en"]
	}

	respondJSON(w, http.StatusOK, bundle)
}

// GetDirection returns "rtl" for Arabic and Hebrew, and "ltr" for all other languages.
func GetDirection(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if len(lang) > 2 {
		lang = lang[:2]
	}
	if lang == "ar" || lang == "he" {
		return "rtl"
	}
	return "ltr"
}

// htmlOpenTagRe matches a document's opening root element tag, with or without attributes.
// Deliberately not anchored: both shells carry a doctype ahead of it.
var htmlOpenTagRe = regexp.MustCompile(`(?is)<html\b[^>]*>`)

// htmlLangOrDirAttrRe matches a `lang=` or `dir=` attribute inside that tag, in any of the three
// spellings HTML allows for a value (double-quoted, single-quoted, bare).
var htmlLangOrDirAttrRe = regexp.MustCompile(`(?is)\s+(?:lang|dir)\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)

// withDocumentLocale rewrites the opening root element tag of doc so that it declares lang and dir.
//
// The portal shells are the two documents a browser parses BEFORE any of this project's JavaScript
// runs, and both shipped a literal lang="en" that stayed wrong until the bundle assigned
// document.documentElement.lang -- #2262 fixed the steady state, #2271 (this) is the window before
// it. A screen reader that has begun announcing a document does not necessarily re-voice it when
// the attribute changes underneath it, so the first thing read after every navigation could be
// translated prose in an English voice, and an RTL layout painted LTR first.
//
// Rewriting the served bytes rather than templating the two files is what keeps locale resolution
// in one place: the caller passes what ResolveLocale/GetDirection already decided, exactly as
// /privacy and /cookies do. The alternative considered and rejected in #2271 was an inline
// <script> reading localStorage ahead of the bundle, which would have been a third and fourth
// implementation of the precedence rule (?lang= > lfr_lang > Accept-Language) with nothing holding
// the copies in step.
//
// Any lang/dir already on the tag is REPLACED, not appended to. HTML's own rule is that the first
// of a duplicated attribute wins, so appending would have been a silent no-op against precisely
// the hardcoded lang="en" this exists to remove. Every other attribute is preserved.
//
// A document with no root element tag is returned unchanged. That case is reachable -- CI's Go
// test jobs stub pkg/server/ui-dist/index.html as an empty file -- and an empty document is not
// something to synthesise a tag into.
func withDocumentLocale(doc, lang, dir string) string {
	loc := htmlOpenTagRe.FindStringIndex(doc)
	if loc == nil {
		return doc
	}
	tag := doc[loc[0]:loc[1]]

	rest := strings.TrimSuffix(tag[len("<html"):], ">")
	rest = strings.TrimSpace(htmlLangOrDirAttrRe.ReplaceAllString(rest, ""))

	rebuilt := `<html lang="` + html.EscapeString(lang) + `" dir="` + html.EscapeString(dir) + `"`
	if rest != "" {
		rebuilt += " " + rest
	}
	rebuilt += ">"

	return doc[:loc[0]] + rebuilt + doc[loc[1]:]
}

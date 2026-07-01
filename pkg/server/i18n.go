package server

import (
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
	locales := []string{"en", "es", "fr", "de", "pt", "ko", "ja", "zh", "ro"}

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
		"en": true, "es": true, "fr": true, "de": true, "pt": true, "ko": true, "ja": true, "zh": true, "ro": true,
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

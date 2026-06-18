package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

//go:embed i18n/*
var i18nFS embed.FS

// initI18n loads all dynamic translation JSON bundles into server memory.
func (s *Server) initI18n() error {
	s.translations = make(map[string]map[string]string)
	locales := []string{"en", "es", "fr", "de", "pt", "ko", "ja", "zh"}

	for _, locale := range locales {
		data, err := i18nFS.ReadFile(fmt.Sprintf("i18n/%s.json", locale))
		if err != nil {
			log.Printf("[i18n] Warning: failed to load JSON bundle for locale %q: %v", locale, err)
			continue
		}

		var bundle map[string]string
		if err := json.Unmarshal(data, &bundle); err != nil {
			return fmt.Errorf("failed to parse JSON bundle for %s: %w", locale, err)
		}

		s.translations[locale] = bundle
	}

	log.Printf("[i18n] Successfully initialized dynamic i18n engine with %d locales.", len(s.translations))
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

// ResolveLocale parses incoming HTTP request headers to extract the best matching locale.
func (s *Server) ResolveLocale(r *http.Request) string {
	// 1. Check Accept-Language header
	acceptLang := r.Header.Get("Accept-Language")
	if acceptLang == "" {
		return "en"
	}

	// Simple parser: e.g. "fr-CH, fr;q=0.9, en;q=0.8"
	parts := strings.Split(acceptLang, ",")
	supported := map[string]bool{
		"en": true, "es": true, "fr": true, "de": true, "pt": true, "ko": true, "ja": true, "zh": true,
	}

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

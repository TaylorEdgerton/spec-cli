package textfilter

import (
	"strings"
	"sync"

	"github.com/bbalet/stopwords"
	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/english"
)

var (
	commonEnglishCache sync.Map
	normalizedCache    sync.Map
)

// IsCommonEnglish reports whether word is in the reusable English stop-word
// set maintained by github.com/bbalet/stopwords.
func IsCommonEnglish(word string) bool {
	word = strings.ToLower(word)
	if cached, ok := commonEnglishCache.Load(word); ok {
		return cached.(bool)
	}
	common := strings.TrimSpace(stopwords.CleanString(word, "en", false)) == ""
	commonEnglishCache.Store(word, common)
	return common
}

// NormalizeEnglish reduces related English word forms to a common search key.
func NormalizeEnglish(word string) string {
	word = strings.ToLower(word)
	cacheKey := word
	if cached, ok := normalizedCache.Load(cacheKey); ok {
		return cached.(string)
	}
	// Snowball deliberately leaves some noun forms distinct. Treat common -ery
	// nouns like "discovery" as their verb family for repository search.
	if len(word) > 5 && strings.HasSuffix(word, "ery") {
		word = strings.TrimSuffix(word, "y")
	}
	environment := snowballstem.NewEnv(word)
	english.Stem(environment)
	normalized := environment.Current()
	normalizedCache.Store(cacheKey, normalized)
	return normalized
}

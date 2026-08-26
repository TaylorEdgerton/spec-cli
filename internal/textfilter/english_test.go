package textfilter

import "testing"

func TestEnglishTextFilteringUsesStandardStopWords(t *testing.T) {
	for _, word := range []string{"the", "with", "would"} {
		if !IsCommonEnglish(word) {
			t.Errorf("%q is not recognised as a common English word", word)
		}
	}
	if IsCommonEnglish("discovery") {
		t.Fatal("discovery should remain a significant search term")
	}
}

func TestNormalizeEnglishGroupsDiscoveryForms(t *testing.T) {
	want := NormalizeEnglish("discover")
	for _, word := range []string{"discovery", "discovering"} {
		if got := NormalizeEnglish(word); got != want {
			t.Errorf("NormalizeEnglish(%q) = %q, want %q", word, got, want)
		}
	}
}

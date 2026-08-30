package grammars

import (
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter"
)

// Language describes the small amount of syntax information the generic
// Tree-sitter symbol provider needs. TagsQuery is the contract between a
// grammar and discovery: definitions and references use the standard
// definition.* and reference.* capture names.
type Language struct {
	Name              string
	Family            string
	Extensions        []string
	Language          func() *gotreesitter.Language
	RootType          string
	TagsQuery         string
	CallType          string
	MemberType        string
	MemberObjectField string
	MemberNameField   string
	FunctionTypes     []string
	ClassTypes        []string
	AssignmentTypes   []string
}

var supported = []Language{Go, Python, JavaScript, TypeScript, TSX}

// ForPath returns the supported grammar for path.
func ForPath(path string) (Language, bool) {
	extension := strings.ToLower(filepath.Ext(path))
	for _, language := range supported {
		for _, candidate := range language.Extensions {
			if extension == candidate {
				return language, true
			}
		}
	}
	return Language{}, false
}

// ForFamily returns every supported grammar that can share repository facts.
func ForFamily(family string) []Language {
	var matches []Language
	for _, language := range supported {
		if language.Family == family {
			matches = append(matches, language)
		}
	}
	return matches
}

func (language Language) IsFunctionType(nodeType string) bool {
	return contains(language.FunctionTypes, nodeType)
}

func (language Language) IsClassType(nodeType string) bool {
	return contains(language.ClassTypes, nodeType)
}

func (language Language) IsAssignmentType(nodeType string) bool {
	return contains(language.AssignmentTypes, nodeType)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

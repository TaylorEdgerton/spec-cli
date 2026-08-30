package grammars

import "github.com/odvcencio/gotreesitter/grammars"

const pythonTagsQuery = `
(function_definition name: (identifier) @name) @definition.function
(class_definition name: (identifier) @name) @definition.class
(call function: (identifier) @name) @reference.call
(call function: (attribute attribute: (identifier) @name)) @reference.call
(identifier) @name @reference.identifier
`

var Python = Language{
	Name: "Python", Family: "python", Extensions: []string{".py"},
	Language: grammars.PythonLanguage, RootType: "module", TagsQuery: pythonTagsQuery,
	CallType: "call", MemberType: "attribute",
	MemberObjectField: "object", MemberNameField: "attribute",
	FunctionTypes:   []string{"function_definition"},
	ClassTypes:      []string{"class_definition"},
	AssignmentTypes: []string{"assignment", "named_expression"},
}

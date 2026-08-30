package grammars

import "github.com/odvcencio/gotreesitter/grammars"

const goTagsQuery = `
(function_declaration name: (identifier) @name) @definition.function
(method_declaration name: (field_identifier) @name) @definition.method
(type_spec name: (type_identifier) @name) @definition.type
(const_spec name: (identifier) @name) @definition.constant
(var_spec name: (identifier) @name) @definition.variable
(call_expression function: (identifier) @name) @reference.call
(call_expression function: (selector_expression field: (field_identifier) @name)) @reference.call
(identifier) @name @reference.identifier
(field_identifier) @name @reference.identifier
(type_identifier) @name @reference.identifier
`

var Go = Language{
	Name: "Go", Family: "go", Extensions: []string{".go"},
	Language: grammars.GoLanguage, RootType: "source_file", TagsQuery: goTagsQuery,
	CallType: "call_expression", MemberType: "selector_expression",
	MemberObjectField: "operand", MemberNameField: "field",
	FunctionTypes:   []string{"function_declaration", "method_declaration"},
	AssignmentTypes: []string{"short_var_declaration", "assignment_statement"},
}

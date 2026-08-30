package grammars

import "github.com/odvcencio/gotreesitter/grammars"

const javaScriptTagsQuery = `
(function_declaration name: (identifier) @name) @definition.function
(method_definition name: (property_identifier) @name) @definition.method
(class_declaration name: (identifier) @name) @definition.class
(variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)]) @definition.function
(call_expression function: (identifier) @name) @reference.call
(call_expression function: (member_expression property: (property_identifier) @name)) @reference.call
(identifier) @name @reference.identifier
(property_identifier) @name @reference.identifier
`

const typeScriptTagsQuery = `
(function_declaration name: (identifier) @name) @definition.function
(method_definition name: (property_identifier) @name) @definition.method
(class_declaration name: (type_identifier) @name) @definition.class
(interface_declaration name: (type_identifier) @name) @definition.interface
(enum_declaration name: (identifier) @name) @definition.type
(type_alias_declaration name: (type_identifier) @name) @definition.type
(variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)]) @definition.function
(call_expression function: (identifier) @name) @reference.call
(call_expression function: (member_expression property: (property_identifier) @name)) @reference.call
(identifier) @name @reference.identifier
(property_identifier) @name @reference.identifier
(type_identifier) @name @reference.identifier
`

var JavaScript = Language{
	Name: "JavaScript", Family: "ecmascript", Extensions: []string{".js", ".jsx", ".mjs", ".cjs"},
	Language: grammars.JavascriptLanguage, RootType: "program", TagsQuery: javaScriptTagsQuery,
	CallType: "call_expression", MemberType: "member_expression",
	MemberObjectField: "object", MemberNameField: "property",
	FunctionTypes:   []string{"function_declaration", "method_definition", "arrow_function", "function_expression"},
	ClassTypes:      []string{"class_declaration"},
	AssignmentTypes: []string{"assignment_expression", "variable_declarator"},
}

var TypeScript = Language{
	Name: "TypeScript", Family: "ecmascript", Extensions: []string{".ts", ".mts", ".cts"},
	Language: grammars.TypescriptLanguage, RootType: "program", TagsQuery: typeScriptTagsQuery,
	CallType: "call_expression", MemberType: "member_expression",
	MemberObjectField: "object", MemberNameField: "property",
	FunctionTypes:   []string{"function_declaration", "method_definition", "arrow_function", "function_expression"},
	ClassTypes:      []string{"class_declaration"},
	AssignmentTypes: []string{"assignment_expression", "variable_declarator"},
}

var TSX = Language{
	Name: "TSX", Family: "ecmascript", Extensions: []string{".tsx"},
	Language: grammars.TsxLanguage, RootType: "program", TagsQuery: typeScriptTagsQuery,
	CallType: "call_expression", MemberType: "member_expression",
	MemberObjectField: "object", MemberNameField: "property",
	FunctionTypes:   []string{"function_declaration", "method_definition", "arrow_function", "function_expression"},
	ClassTypes:      []string{"class_declaration"},
	AssignmentTypes: []string{"assignment_expression", "variable_declarator"},
}

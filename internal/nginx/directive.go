// Package nginx models OpenResty/nginx configuration as a tree of directives
// and renders that tree, with variable substitution, into configuration text.
package nginx

import (
	"fmt"
	"regexp"
	"strings"
)

// Directive is a single nginx directive. A directive with Children (or Block
// set) renders as a block `name args { ... }`. A directive with Raw renders as
// a block whose body is copied verbatim, which is what OpenResty's
// *_by_lua_block directives need.
type Directive struct {
	ID       int64       `json:"id,omitempty"`
	ParentID *int64      `json:"parent_id,omitempty"`
	Position int         `json:"position"`
	Name     string      `json:"name"`
	Args     []string    `json:"args,omitempty"`
	Block    bool        `json:"block,omitempty"`
	Raw      *string     `json:"raw,omitempty"`
	Comment  string      `json:"comment,omitempty"`
	Children []Directive `json:"children,omitempty"`
}

// IsBlock reports whether the directive renders with braces.
func (d *Directive) IsBlock() bool {
	return d.Block || len(d.Children) > 0 || d.Raw != nil
}

// Variable is a placeholder a template declares. Placeholders are written as
// {{name}} inside directive args or raw bodies.
type Variable struct {
	Name        string  `json:"name"`
	Default     *string `json:"default,omitempty"`
	Required    bool    `json:"required"`
	Description string  `json:"description,omitempty"`
}

const (
	maxDepth      = 32
	maxDirectives = 10000
)

var (
	directiveNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	variableNameRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// ResourceNameRe constrains names that end up in routing keys or file
	// names: no dots (topic separators), no slashes (path traversal).
	ResourceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
)

// ValidationError describes why a tree or variable set was rejected.
type ValidationError struct {
	Path string
	Msg  string
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return e.Msg
	}
	return e.Path + ": " + e.Msg
}

func invalid(path, format string, a ...any) error {
	return &ValidationError{Path: path, Msg: fmt.Sprintf(format, a...)}
}

// ValidateTree checks every directive in the forest.
func ValidateTree(ds []Directive) error {
	count := 0
	return validateLevel(ds, "directives", 1, &count)
}

// ValidateDirective checks a single directive and its subtree.
func ValidateDirective(d *Directive) error {
	count := 0
	return validateOne(d, "directive", 1, &count)
}

func validateLevel(ds []Directive, path string, depth int, count *int) error {
	for i := range ds {
		if err := validateOne(&ds[i], fmt.Sprintf("%s[%d]", path, i), depth, count); err != nil {
			return err
		}
	}
	return nil
}

func validateOne(d *Directive, path string, depth int, count *int) error {
	*count++
	if *count > maxDirectives {
		return invalid("", "too many directives (max %d)", maxDirectives)
	}
	if depth > maxDepth {
		return invalid(path, "nesting too deep (max %d)", maxDepth)
	}
	if !directiveNameRe.MatchString(d.Name) {
		return invalid(path, "invalid directive name %q", d.Name)
	}
	if d.Raw != nil && len(d.Children) > 0 {
		return invalid(path, "a directive cannot have both raw and children")
	}
	if strings.ContainsAny(d.Comment, "\r\n") {
		return invalid(path, "comment must be a single line")
	}
	for j, a := range d.Args {
		// Placeholders are checked again after substitution at render time.
		if err := validateArg(placeholderRe.ReplaceAllString(a, "x")); err != nil {
			return invalid(fmt.Sprintf("%s.args[%d]", path, j), "%s", err)
		}
	}
	return validateLevel(d.Children, path+".children", depth+1, count)
}

// validateArg rejects arguments that would break out of their directive:
// structural characters are only allowed inside a quoted argument.
func validateArg(a string) error {
	if a == "" {
		return fmt.Errorf("empty argument")
	}
	if strings.ContainsAny(a, "\r\n") {
		return fmt.Errorf("argument must not contain newlines")
	}
	if isQuoted(a) {
		return nil
	}
	if strings.ContainsAny(a, ";{}") || strings.ContainsAny(a, " \t") {
		return fmt.Errorf("argument %q contains whitespace or one of ;{} and must be quoted", a)
	}
	return nil
}

func isQuoted(a string) bool {
	if len(a) < 2 {
		return false
	}
	q := a[0]
	if (q != '"' && q != '\'') || a[len(a)-1] != q {
		return false
	}
	// The closing quote must not be escaped.
	backslashes := 0
	for i := len(a) - 2; i > 0 && a[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 0
}

// ValidateVariables checks variable declarations for a template.
func ValidateVariables(vs []Variable) error {
	seen := make(map[string]bool, len(vs))
	for i, v := range vs {
		path := fmt.Sprintf("variables[%d]", i)
		if !variableNameRe.MatchString(v.Name) {
			return invalid(path, "invalid variable name %q", v.Name)
		}
		if seen[v.Name] {
			return invalid(path, "duplicate variable %q", v.Name)
		}
		seen[v.Name] = true
		if v.Default != nil {
			if err := validateValue(*v.Default); err != nil {
				return invalid(path+".default", "%s", err)
			}
		}
	}
	return nil
}

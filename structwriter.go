package zek

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"reflect"
	"strings"
	"time"
)

var (
	// UppercaseByDefault is used during XML tag name to Go name conversion.
	UppercaseByDefault = []string{"id", "isbn", "ismn", "eissn", "issn", "lccn", "rfc", "rsn", "uri", "url", "urn", "zdb"}
	// DefaultTextField is used to hold character data.
	DefaultTextField = "Text"
	// DefaultAttributePrefixes are used, if there are name clashes.
	DefaultAttributePrefixes = []string{"Attr", "Attribute"}
)

type stickyErrWriter struct {
	w   io.Writer
	err *error
}

// Write returns early, if write has failed in the past.
func (sew stickyErrWriter) Write(p []byte) (n int, err error) {
	if *sew.err != nil {
		return 0, *sew.err
	}
	n, err = sew.w.Write(p)
	*sew.err = err
	return
}

// stringSliceContains returns true, if a string is found in a slice.
func stringSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// truncateString after n chars and append ellipsis.
func truncateString(s string, n int, ellipsis string) string {
	if len(s) < n {
		return s
	}
	return fmt.Sprintf("%s%s", s[:n], ellipsis)
}

// CreateNameFunc returns a function that converts a tag into a canonical Go
// name. Given list of strings will be wholly upper cased.
func CreateNameFunc(upper []string) func(string) string {
	f := func(name string) string {
		var capped []string
		splitter := func(c rune) bool {
			return c == '_' || c == '-' || c == '.'
		}
		for _, s := range strings.FieldsFunc(name, splitter) {
			switch {
			case stringSliceContains(upper, strings.ToLower(s)):
				capped = append(capped, strings.ToUpper(s))
			default:
				capped = append(capped, strings.Title(s))
			}
		}
		return strings.Join(capped, "")
	}
	return f
}

// StructWriter can turn a node into a struct and can be configured.
type StructWriter struct {
	w                 io.Writer
	NameFunc          func(string) string // Turns xml tag names into Go names.
	TextFieldName     string              // Field name for chardata.
	AttributePrefixes []string            // In case of a name clash, try these prefixes.
	WithComments      bool                // Annotate struct with comments and examples.
	Banner            string              // Autogenerated note.
}

// NewStructWriter can write a node to a given writer. Default list of
// abbreviations to wholly uppercase.
func NewStructWriter(w io.Writer) *StructWriter {
	// Some info for banner.
	usr, err := user.Current()
	if err != nil {
		usr.Name = "an unknown user"
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "an unknown host"
	}
	banner := fmt.Sprintf("generated %s by %s on %s.",
		time.Now().Format("2006-01-02 15:04:05"), usr.Username, hostname)

	return &StructWriter{
		w:                 w,
		NameFunc:          CreateNameFunc(UppercaseByDefault),
		TextFieldName:     DefaultTextField,
		AttributePrefixes: DefaultAttributePrefixes,
		Banner:            banner,
	}
}

// WriteNode writes a node to a writer.
func (sw *StructWriter) WriteNode(node *Node) (err error) {
	if sw.w == nil {
		return nil
	}
	if node == nil || reflect.DeepEqual(node, new(Node)) {
		return nil
	}
	return sw.writeNode(node, true)
}

// writeField writes a field with a simple xml struct tag to writer.
func (sw *StructWriter) writeNameField(w io.Writer, node *Node) (int, error) {
	return fmt.Fprintf(w, "XMLName xml.Name `xml:\"%s\"`\n", node.Name.Local)
}

// writeChardataField writes a chardata field. Might add a comment as well.
func (sw *StructWriter) writeChardataField(w io.Writer, node *Node) (int, error) {
	// XXX: Check, if there is any child or attribute with this name.
	s := fmt.Sprintf("%s string `xml:\",chardata\"`", sw.TextFieldName)
	if sw.WithComments && len(node.Examples) > 0 {
		examples := strings.Replace(strings.Join(node.Examples, ", "), "\n", " ", -1)
		s = fmt.Sprintf("%s // %s", s, truncateString(examples, 25, "..."))
	}
	return fmt.Fprintf(w, "%s\n", s)
}

// writeAttrField writes an attribute field.
func (sw *StructWriter) writeAttrField(w io.Writer, name, typeName, tag string) (int, error) {
	return fmt.Fprintf(w, "%s %s `xml:\"%s,attr\"`\n", name, typeName, tag)
}

// writeStructTag writes xml tag at the end of struct declaration.
func (sw *StructWriter) writeStructTag(w io.Writer, node *Node) (int, error) {
	return fmt.Fprintf(w, "`xml:\"%s\"`", node.Name.Local)
}

// writeNode writes out the node as a struct. Output is not formatted.
func (sw *StructWriter) writeNode(node *Node, top bool) (err error) {
	sew := stickyErrWriter{w: sw.w, err: &err}
	if top {
		if sw.Banner != "" {
			io.WriteString(sew, fmt.Sprintf("// %s was %s\n",
				sw.NameFunc(node.Name.Local), sw.Banner))
		}
		io.WriteString(sew, "type ")
	}
	io.WriteString(sew, sw.NameFunc(node.Name.Local))
	io.WriteString(sew, " ")
	if node.IsMultivalued() && !top {
		io.WriteString(sew, "[]")
	}
	io.WriteString(sew, "struct {\n")
	if top {
		sw.writeNameField(sew, node)
	}
	sw.writeChardataField(sew, node)

	// Helper to check for name clash with any generated field name.
	isValidName := func(name string) bool {
		if name == sw.TextFieldName {
			return false
		}
		for _, child := range node.Children {
			if name == sw.NameFunc(child.Name.Local) {
				return false
			}
		}
		return true
	}

	// Write attributes.
	for _, attr := range node.Attr {
		name := sw.NameFunc(attr.Name.Local)
		for _, prefix := range sw.AttributePrefixes {
			if isValidName(name) {
				break
			}
			name = fmt.Sprintf("%s%s", prefix, name)
		}
		if !isValidName(name) {
			return fmt.Errorf("name clash: %s", attr.Name.Local)
		}
		sw.writeAttrField(sew, name, "string", attr.Name.Local)
	}

	// Write children.
	for _, child := range node.Children {
		sw.writeNode(child, false)
	}

	// Write outro.
	io.WriteString(sew, "} ")
	if !top {
		sw.writeStructTag(sew, node)
	}
	io.WriteString(sew, "\n")
	return err
}

package validationfile

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	v0 "github.com/authzed/authzed-go/proto/authzed/api/v0"
	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	yaml "gopkg.in/yaml.v2"
	yamlv3 "gopkg.in/yaml.v3"

	"github.com/authzed/spicedb/pkg/tuple"
)

// ErrorWithSource is an error that includes the source text and position
// information.
type ErrorWithSource struct {
	error

	// Source is the source text for the error.
	Source string

	// LineNumber is the (1-indexed) line number of the error, or 0 if unknown.
	LineNumber uint32

	// ColumnPosition is the (1-indexed) column position of the error, or 0 if
	// unknown.
	ColumnPosition uint32
}

// ParseValidationBlock attempts to parse the given contents as a YAML
// validation block.
func ParseValidationBlock(contents []byte) (ValidationMap, error) {
	// TODO(jschorr): proper manual decode to retain line and col info.
	block := ValidationMap{}
	err := yamlv3.Unmarshal(contents, &block)
	return block, err
}

// DecodeValidationBlock attempts to decode the given contents as a YAML
// validation block.
func DecodeValidationBlock(node yamlv3.Node) (ValidationMap, error) {
	// TODO(jschorr): proper manual decode to retain line and col info.
	contents, err := yamlv3.Marshal(node)
	if err != nil {
		return nil, err
	}

	block := ValidationMap{}
	err = yamlv3.Unmarshal(contents, &block)
	return block, err
}

// ParseAssertionsBlock attempts to parse the given contents as a YAML
// assertions block.
func ParseAssertionsBlock(contents []byte) (*Assertions, error) {
	if len(strings.TrimSpace(string(contents))) == 0 {
		return &Assertions{}, nil
	}

	// Unmarshal to a node, so we can get line and col information.
	var node yamlv3.Node
	err := yamlv3.Unmarshal(contents, &node)
	if err != nil {
		return nil, err
	}

	if len(node.Content) == 0 {
		return &Assertions{}, nil
	}

	if node.Content[0].Kind != yamlv3.MappingNode {
		return nil, fmt.Errorf("expected object at top level")
	}

	mapping := node.Content[0]
	return DecodeAssertionsBlock(mapping)
}

// DecodeAssertionsBlock attempts to decode the given contents as a YAML
// assertions block.
func DecodeAssertionsBlock(mapping *yamlv3.Node) (*Assertions, error) {
	key := ""
	var parsed Assertions
	for _, child := range mapping.Content {
		if child.Kind == yamlv3.ScalarNode {
			key = child.Value

			switch key {
			case "assertTrue":
			case "assertFalse":
				continue

			default:
				return nil, ErrorWithSource{
					fmt.Errorf("unexpected key `%s` on line %d", key, child.Line),
					key,
					uint32(child.Line),
					uint32(child.Column),
				}
			}
		}

		if child.Kind == yamlv3.SequenceNode {
			for _, contentChild := range child.Content {
				if contentChild.Kind == yamlv3.ScalarNode {
					assertion := Assertion{
						relationshipString: contentChild.Value,
						lineNumber:         contentChild.Line,
						columnPosition:     contentChild.Column,
					}

					switch key {
					case "assertTrue":
						parsed.AssertTrue = append(parsed.AssertTrue, assertion)

					case "assertFalse":
						parsed.AssertFalse = append(parsed.AssertFalse, assertion)

					default:
						return nil, ErrorWithSource{
							fmt.Errorf("unexpected key `%s` on line %d", key, child.Line),
							key,
							uint32(child.Line),
							uint32(child.Column),
						}
					}
				} else {
					return nil, fmt.Errorf("unexpected value on line `%d`", contentChild.Line)
				}
			}
		}
	}

	// Marshal and then unmarshal to a well-typed block, in case manual error checking missed something.
	encoded, err := yamlv3.Marshal(mapping)
	if err != nil {
		return nil, err
	}

	var simple SimpleAssertions
	err = yamlv3.Unmarshal(encoded, &simple)
	if err != nil {
		return nil, err
	}

	return &parsed, nil
}

// ValidationMap is a map from an Object Relation (as a Relationship) to the
// validation strings containing the Subjects for that Object Relation.
type ValidationMap map[ObjectRelationString][]ValidationString

// AsYAML returns the ValidationMap in its YAML form.
func (vm ValidationMap) AsYAML() (string, error) {
	// NOTE: We use yaml here instead of yamlv3, as it formats maps differently
	// in terms of indentation.
	// TODO(jschorr): Change this if/when we don't mind the formatting being slightly
	// different.
	data, err := yaml.Marshal(vm)
	return string(data), err
}

// ObjectRelationString represents an ONR defined as a string in the key for
// the ValidationMap.
type ObjectRelationString string

// ONR returns the ObjectAndRelation parsed from this string, if valid, or an
// error on failure to parse.
func (ors ObjectRelationString) ONR() (*v0.ObjectAndRelation, *ErrorWithSource) {
	parsed := tuple.ParseONR(string(ors))
	if parsed == nil {
		return nil, &ErrorWithSource{fmt.Errorf("could not parse %s", ors), string(ors), 0, 0}
	}
	return parsed, nil
}

var (
	vsSubjectRegex               = regexp.MustCompile(`(.*?)\[(?P<user_str>.*)\](.*?)`)
	vsObjectAndRelationRegex     = regexp.MustCompile(`(.*?)<(?P<onr_str>[^\>]+)>(.*?)`)
	vsSubjectWithExceptionsRegex = regexp.MustCompile(`^(.+)\s*-\s*\{([^\}]+)\}$`)
)

// SubjectWithExceptions returns the subject found in a validation string, along with any exceptions.
type SubjectWithExceptions struct {
	// Subject is the subject found.
	Subject *v0.ObjectAndRelation

	// Exceptions are those subjects removed from the subject, if it is a wildcard.
	Exceptions []*v0.ObjectAndRelation
}

// ValidationString holds a validation string containing a Subject and one or
// more Relations to the parent Object.
// Example: `[tenant/user:someuser#...] is <tenant/document:example#viewer>`
type ValidationString string

// SubjectString returns the subject contained in the ValidationString, if any.
func (vs ValidationString) SubjectString() (string, bool) {
	result := vsSubjectRegex.FindStringSubmatch(string(vs))
	if len(result) != 4 {
		return "", false
	}

	return result[2], true
}

// Subject returns the subject contained in the ValidationString, if any. If
// none, returns nil.
func (vs ValidationString) Subject() (*SubjectWithExceptions, *ErrorWithSource) {
	subjectStr, ok := vs.SubjectString()
	if !ok {
		return nil, nil
	}

	subjectStr = strings.TrimSpace(subjectStr)
	if strings.HasSuffix(subjectStr, "}") {
		result := vsSubjectWithExceptionsRegex.FindStringSubmatch(subjectStr)
		if len(result) != 3 {
			return nil, &ErrorWithSource{fmt.Errorf("invalid subject: %s", subjectStr), subjectStr, 0, 0}
		}

		subjectONR := tuple.ParseSubjectONR(strings.TrimSpace(result[1]))
		if subjectONR == nil {
			return nil, &ErrorWithSource{fmt.Errorf("invalid subject: %s", result[1]), result[1], 0, 0}
		}

		exceptionsString := strings.TrimSpace(result[2])
		exceptionsStringsSlice := strings.Split(exceptionsString, ",")
		exceptions := make([]*v0.ObjectAndRelation, 0, len(exceptionsStringsSlice))
		for _, exceptionString := range exceptionsStringsSlice {
			exceptionONR := tuple.ParseSubjectONR(strings.TrimSpace(exceptionString))
			if exceptionONR == nil {
				return nil, &ErrorWithSource{fmt.Errorf("invalid subject: %s", exceptionString), exceptionString, 0, 0}
			}

			exceptions = append(exceptions, exceptionONR)
		}

		return &SubjectWithExceptions{subjectONR, exceptions}, nil
	}

	found := tuple.ParseSubjectONR(subjectStr)
	if found == nil {
		return nil, &ErrorWithSource{fmt.Errorf("invalid subject: %s", subjectStr), subjectStr, 0, 0}
	}
	return &SubjectWithExceptions{found, nil}, nil
}

// ONRStrings returns the ONRs contained in the ValidationString, if any.
func (vs ValidationString) ONRStrings() []string {
	results := vsObjectAndRelationRegex.FindAllStringSubmatch(string(vs), -1)
	onrStrings := []string{}
	for _, result := range results {
		onrStrings = append(onrStrings, result[2])
	}
	return onrStrings
}

// ONRS returns the subject ONRs in the ValidationString, if any.
func (vs ValidationString) ONRS() ([]*v0.ObjectAndRelation, *ErrorWithSource) {
	onrStrings := vs.ONRStrings()

	onrs := []*v0.ObjectAndRelation{}
	for _, onrString := range onrStrings {
		found := tuple.ParseONR(onrString)
		if found == nil {
			return nil, &ErrorWithSource{fmt.Errorf("invalid object and relation: %s", onrString), onrString, 0, 0}
		}

		onrs = append(onrs, found)
	}
	return onrs, nil
}

// SimpleAssertions is a parsed assertions block.
type SimpleAssertions struct {
	// AssertTrue is the set of relationships to assert true.
	AssertTrue []string `yaml:"assertTrue"`

	// AssertFalse is the set of relationships to assert false.
	AssertFalse []string `yaml:"assertFalse"`
}

// Assertion is an unparsed assertion.
type Assertion struct {
	relationshipString string
	lineNumber         int
	columnPosition     int
}

// Assertions represents assertions defined in the validation file.
type Assertions struct {
	// AssertTrue is the set of relationships to assert true.
	AssertTrue []Assertion `yaml:"assertTrue"`

	// AssertFalse is the set of relationships to assert false.
	AssertFalse []Assertion `yaml:"assertFalse"`
}

// ParsedAssertion contains information about a parsed assertion relationship.
type ParsedAssertion struct {
	// Relationship is the parsed relationship on which the assertion is being
	// run.
	Relationship *v0.RelationTuple

	// LineNumber is the (1-indexed) line number of the assertion in the parent
	// YAML.
	LineNumber uint32

	// ColumnPosition is the (1-indexed) column position of the assertion in the
	// parent YAML.
	ColumnPosition uint32
}

// AssertTrueRelationships returns the relationships for which to assert
// existence.
func (a *Assertions) AssertTrueRelationships() ([]ParsedAssertion, *ErrorWithSource) {
	if a == nil {
		return []ParsedAssertion{}, nil
	}

	relationships := make([]ParsedAssertion, 0, len(a.AssertTrue))
	for _, assertion := range a.AssertTrue {
		trimmed := strings.TrimSpace(assertion.relationshipString)
		parsed := tuple.Parse(trimmed)
		if parsed == nil {
			return relationships, &ErrorWithSource{
				fmt.Errorf("could not parse relationship `%s`", assertion.relationshipString),
				assertion.relationshipString,
				uint32(assertion.lineNumber),
				uint32(assertion.columnPosition),
			}
		}
		relationships = append(relationships, ParsedAssertion{
			Relationship:   parsed,
			LineNumber:     uint32(assertion.lineNumber),
			ColumnPosition: uint32(assertion.columnPosition),
		})
	}
	return relationships, nil
}

// AssertFalseRelationships returns the relationships for which to assert
// non-existence.
func (a *Assertions) AssertFalseRelationships() ([]ParsedAssertion, *ErrorWithSource) {
	if a == nil {
		return []ParsedAssertion{}, nil
	}

	relationships := make([]ParsedAssertion, 0, len(a.AssertFalse))
	for _, assertion := range a.AssertFalse {
		trimmed := strings.TrimSpace(assertion.relationshipString)
		parsed := tuple.Parse(trimmed)
		if parsed == nil {
			return relationships, &ErrorWithSource{
				fmt.Errorf("could not parse relationship `%s`", assertion.relationshipString),
				assertion.relationshipString,
				uint32(assertion.lineNumber),
				uint32(assertion.columnPosition),
			}
		}
		relationships = append(relationships, ParsedAssertion{
			Relationship:   parsed,
			LineNumber:     uint32(assertion.lineNumber),
			ColumnPosition: uint32(assertion.columnPosition),
		})
	}
	return relationships, nil
}

// ParseRelationshipsBlock parses a block of newline-separated relationships into a set
// of relation tuples.
func ParseRelationshipsBlock(contents []byte) ([]*v1.Relationship, error) {
	// Unmarshal to a node, so we can get line and col information.
	var node yamlv3.Node
	err := yamlv3.Unmarshal(contents, &node)
	if err != nil {
		return nil, err
	}

	if len(node.Content) == 0 {
		return nil, nil
	}

	if node.Content[0].Kind != yamlv3.ScalarNode {
		return nil, fmt.Errorf("expected string at top level")
	}

	return DecodeRelationshipsBlock(node.Value, node.Line)
}

// DecodeRelationshipsBlock decodes a YAML node containing a block of newline-separated
// relationships into a set of relation tuples.
func DecodeRelationshipsBlock(value string, startingLineIndex int) ([]*v1.Relationship, error) {
	parsed, err := ParseRelationships(value)
	if err != nil {
		var errWithSource ErrorWithSource
		if errors.As(err, &errWithSource) {
			return nil, ErrorWithSource{
				errWithSource.error,
				errWithSource.Source,
				errWithSource.LineNumber + uint32(startingLineIndex),
				errWithSource.ColumnPosition,
			}
		}
	}
	return parsed, err
}

// ParseRelationships parses a newline-separated relationships string into a set
// of relation tuples.
func ParseRelationships(relationshipsString string) ([]*v1.Relationship, error) {
	if relationshipsString == "" {
		return []*v1.Relationship{}, nil
	}

	seenTuples := map[string]bool{}
	lines := strings.Split(relationshipsString, "\n")
	relationships := make([]*v1.Relationship, 0, len(lines))
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) == 0 || strings.HasPrefix(trimmed, "//") {
			continue
		}

		tpl := tuple.Parse(trimmed)
		if tpl == nil {
			return nil, ErrorWithSource{
				fmt.Errorf("error parsing relationship #%v: %s", index, trimmed),
				trimmed,
				uint32(index + 1), // 1-indexed
				1,                 // 1-indexed
			}
		}

		_, ok := seenTuples[tuple.String(tpl)]
		if ok {
			continue
		}
		seenTuples[tuple.String(tpl)] = true
		relationships = append(relationships, tuple.MustToRelationship(tpl))
	}

	return relationships, nil
}
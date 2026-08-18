package vault

import (
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/schema"
)

// TestParseFrontmatterTolerance covers the shapes a hand-edited or
// Obsidian-written frontmatter block can legitimately arrive in, and the
// shapes that are wrong enough to report but not wrong enough to abort.
func TestParseFrontmatterTolerance(t *testing.T) {
	tests := []struct {
		name       string
		block      string
		wantFields []string // fields expected to produce an issue
		check      func(*testing.T, schema.Frontmatter)
	}{
		{
			name:  "unquoted date is read as a timestamp by yaml",
			block: "type: session\ncreated: 2026-08-17\nupdated: 2026-08-17\nauthor: agent",
			check: func(t *testing.T, fm schema.Frontmatter) {
				if fm.Created.String() != "2026-08-17" {
					t.Errorf("created = %q, want 2026-08-17", fm.Created)
				}
			},
		},
		{
			name:  "quoted date parses the same way",
			block: `type: session` + "\n" + `created: "2026-08-17"` + "\n" + `updated: "2026-08-17"`,
			check: func(t *testing.T, fm schema.Frontmatter) {
				if fm.Created.String() != "2026-08-17" {
					t.Errorf("created = %q, want 2026-08-17", fm.Created)
				}
			},
		},
		{
			name:       "date of the wrong type is an issue",
			block:      "type: session\ncreated: 17\nupdated: true",
			wantFields: []string{"created", "updated"},
		},
		{
			name:  "flow and block tag lists both work",
			block: "tags: [one, two]",
			check: func(t *testing.T, fm schema.Frontmatter) {
				if strings.Join(fm.Tags, ",") != "one,two" {
					t.Errorf("tags = %v, want [one two]", fm.Tags)
				}
			},
		},
		{
			name:  "null list is empty rather than an issue",
			block: "tags:\nrelated:",
			check: func(t *testing.T, fm schema.Frontmatter) {
				if len(fm.Tags) != 0 || len(fm.Related) != 0 {
					t.Errorf("tags/related = %v/%v, want both empty", fm.Tags, fm.Related)
				}
			},
		},
		{
			name:       "list containing a non-string is an issue",
			block:      "tags: [one, 2, three]",
			wantFields: []string{"tags"},
		},
		{
			name:       "mapping where a list belongs is an issue",
			block:      "related:\n  key: value",
			wantFields: []string{"related"},
		},
		{
			name:       "list where a scalar belongs is an issue",
			block:      "type: [session]\nstatus: [complete]",
			wantFields: []string{"type", "status"},
		},
		{
			name:  "unmanaged keys are kept",
			block: "type: session\naliases: [Other Name]\ncount: 3",
			check: func(t *testing.T, fm schema.Frontmatter) {
				if _, ok := fm.Extra["aliases"]; !ok {
					t.Errorf("extra = %v, want it to hold aliases", fm.Extra)
				}
				if fm.Extra["count"] == nil {
					t.Errorf("extra = %v, want it to hold count", fm.Extra)
				}
			},
		},
		{
			name:  "empty block yields empty frontmatter",
			block: "",
			check: func(t *testing.T, fm schema.Frontmatter) {
				if fm.Type != "" || len(fm.Extra) != 0 {
					t.Errorf("frontmatter = %+v, want zero value", fm)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fm, issues, err := parseFrontmatter(tc.block, false)
			if err != nil {
				t.Fatalf("parseFrontmatter: %v", err)
			}

			var got []string
			for _, i := range issues {
				got = append(got, i.Field)
			}
			if strings.Join(got, ",") != strings.Join(tc.wantFields, ",") {
				t.Errorf("issue fields = %v, want %v (issues: %+v)", got, tc.wantFields, issues)
			}
			if tc.check != nil {
				tc.check(t, fm)
			}
		})
	}
}

// TestParseFrontmatterCoercion covers the importer's tolerant mode: a bare
// number or boolean where a string list is expected is read as its string
// form rather than reported, while strict parsing still reports it.
func TestParseFrontmatterCoercion(t *testing.T) {
	tests := []struct {
		name     string
		block    string
		wantTags string
	}{
		{name: "numeric list entry", block: "tags: [pp, 7695, query]", wantTags: "pp,7695,query"},
		{name: "boolean list entry", block: "tags:\n  - one\n  - true", wantTags: "one,true"},
		{name: "bare number", block: "tags: 207", wantTags: "207"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Strict parsing reports the bad entry and drops the field.
			if _, issues, err := parseFrontmatter(tc.block, false); err != nil {
				t.Fatalf("parseFrontmatter(strict): %v", err)
			} else if len(issues) == 0 {
				t.Errorf("strict parse of %q reported no issue", tc.block)
			}

			// Coercing parsing accepts it as strings.
			fm, issues, err := parseFrontmatter(tc.block, true)
			if err != nil {
				t.Fatalf("parseFrontmatter(coerce): %v", err)
			}
			if len(issues) != 0 {
				t.Errorf("coercing parse of %q reported issues: %+v", tc.block, issues)
			}
			if got := strings.Join(fm.Tags, ","); got != tc.wantTags {
				t.Errorf("tags = %q, want %q", got, tc.wantTags)
			}
		})
	}
}

// TestCoercionNormalizesTagShape covers the importer reshaping tags a source
// vault wrote in forms the schema forbids, keeping the meaning while making
// them uniform. Strict parsing leaves them untouched — tag shape is a lint
// concern there, not a parse one.
func TestCoercionNormalizesTagShape(t *testing.T) {
	tests := []struct {
		block    string
		wantTags string
	}{
		{block: "tags: [PP-7819, account_aggregation]", wantTags: "pp-7819,account-aggregation"},
		{block: "tags: [CDK, EKS]", wantTags: "cdk,eks"},
		{block: "tags: [logTurnstileRequest, gpt-5.6-sol]", wantTags: "logturnstilerequest,gpt-5-6-sol"},
		{block: "tags: [already-fine, s3SDK]", wantTags: "already-fine,s3sdk"},
	}

	for _, tc := range tests {
		t.Run(tc.block, func(t *testing.T) {
			fm, issues, err := parseFrontmatter(tc.block, true)
			if err != nil {
				t.Fatalf("parseFrontmatter(coerce): %v", err)
			}
			if len(issues) != 0 {
				t.Errorf("coercing parse reported issues: %+v", issues)
			}
			if got := strings.Join(fm.Tags, ","); got != tc.wantTags {
				t.Errorf("tags = %q, want %q", got, tc.wantTags)
			}
		})
	}
}

// TestCoercionMapsStatusSynonyms covers the importer folding a hand-kept
// vault's free-form status values onto the closed set the schema allows, while
// leaving an unrecognised value for lint to report.
func TestCoercionMapsStatusSynonyms(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "completed", want: "complete"},
		{in: "verified-fix", want: "complete"},
		{in: "verified-closed", want: "complete"},
		{in: "complete", want: "complete"},
		{in: "in-progress", want: "in-progress"},
		{in: "bananas", want: "bananas"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			fm, _, err := parseFrontmatter("status: "+tc.in, true)
			if err != nil {
				t.Fatalf("parseFrontmatter: %v", err)
			}
			if string(fm.Status) != tc.want {
				t.Errorf("status = %q, want %q", fm.Status, tc.want)
			}
		})
	}

	// Strict parsing does not remap.
	if fm, _, _ := parseFrontmatter("status: completed", false); string(fm.Status) != "completed" {
		t.Errorf("strict status = %q, want it left as completed", fm.Status)
	}
}

func TestParseFrontmatterRejectsInvalidYAML(t *testing.T) {
	if _, _, err := parseFrontmatter("type: session\n  bad indent: [unclosed", false); err == nil {
		t.Error("parseFrontmatter accepted invalid YAML, want error")
	}
}

// TestIssueMessagesNameTheType checks that a type mismatch tells the author
// what it found, since "expected a list" alone does not say what to fix.
func TestIssueMessagesNameTheType(t *testing.T) {
	tests := []struct {
		block string
		want  string
	}{
		{block: "tags: 3", want: "a number"},
		{block: "tags: true", want: "a boolean"},
		{block: "tags:\n  key: value", want: "a mapping"},
		{block: "type: [a]", want: "a list"},
		{block: "type: 3", want: "a number"},
		{block: "created: [2026-08-17]", want: "a list"},
	}

	for _, tc := range tests {
		t.Run(tc.want+" in "+tc.block, func(t *testing.T) {
			_, issues, err := parseFrontmatter(tc.block, false)
			if err != nil {
				t.Fatalf("parseFrontmatter: %v", err)
			}
			if len(issues) == 0 {
				t.Fatal("no issues reported")
			}
			if !strings.Contains(issues[0].Message, tc.want) {
				t.Errorf("message = %q, want it to mention %q", issues[0].Message, tc.want)
			}
		})
	}
}

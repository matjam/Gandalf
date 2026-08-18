package server

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/schema"
)

// LintInput selects what to validate.
type LintInput struct {
	Ref string `json:"ref,omitempty" jsonschema:"a single note to check; omit to check the whole vault"`
}

// Finding is one lint result, addressed by ref rather than by path.
type Finding struct {
	Ref      string `json:"ref"`
	Line     int    `json:"line,omitempty"`
	Field    string `json:"field,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// LintOutput is the result of a validation run.
type LintOutput struct {
	Findings []Finding `json:"findings"`
	Errors   int       `json:"errors"`
	Warnings int       `json:"warnings"`
	Clean    bool      `json:"clean"`
}

// lint validates one note or the whole vault.
func (s *Server) lint(ctx context.Context, _ *sdk.CallToolRequest, in LintInput) (*sdk.CallToolResult, LintOutput, error) {
	var paths []string
	if in.Ref != "" {
		_, path, err := s.resolve(in.Ref)
		if err != nil {
			return nil, LintOutput{}, err
		}
		paths = append(paths, path)
	}

	findings, err := s.vault.Lint(paths...)
	if err != nil {
		return nil, LintOutput{}, err
	}

	layout := s.vault.Layout()
	out := LintOutput{Findings: make([]Finding, 0, len(findings))}

	for _, f := range findings {
		out.Findings = append(out.Findings, Finding{
			Ref:      layout.RefFor(f.Path).String(),
			Line:     f.Line,
			Field:    f.Field,
			Severity: string(f.Severity),
			Message:  f.Message,
		})
		if f.Severity == schema.SeverityError {
			out.Errors++
		} else {
			out.Warnings++
		}
	}

	out.Clean = len(out.Findings) == 0

	return nil, out, nil
}

// CorrectInput records a correction from the user.
type CorrectInput struct {
	Target string `json:"target" jsonschema:"where the rule belongs: contract, topic:<name>, or standard:<name>"`

	Guidance string `json:"guidance" jsonschema:"the rule, stated as an instruction to follow in future"`

	Reason string `json:"reason,omitempty" jsonschema:"what prompted the correction; recorded in the correction history, not in the rule"`
}

// CorrectOutput reports where the correction went.
type CorrectOutput struct {
	Ref     string `json:"ref"`
	History string `json:"history_ref,omitempty"`
}

// correct writes a correction into the one document that owns that kind of
// guidance, and its reasoning into the correction history.
//
// Splitting the two is the point. A rule has to be read every session, so it
// belongs in the contract; the incident that produced it does not, so it
// belongs in the history, where it stops the same argument recurring without
// costing context every time.
func (s *Server) correct(ctx context.Context, _ *sdk.CallToolRequest, in CorrectInput) (*sdk.CallToolResult, CorrectOutput, error) {
	if in.Guidance == "" {
		return nil, CorrectOutput{}, fmt.Errorf("a correction needs guidance to record")
	}

	target := in.Target
	if target == "" || target == "contract" {
		target = "topic:operating"
	}

	ref, path, err := s.writable(target)
	if err != nil {
		return nil, CorrectOutput{}, fmt.Errorf("correction target %q: %w", in.Target, err)
	}

	note, err := s.vault.Read(path)
	if err != nil {
		return nil, CorrectOutput{}, err
	}
	note.Append("", fmt.Sprintf("- %s", in.Guidance))
	note.Touch(schema.Today())

	if err := s.vault.Write(note); err != nil {
		return nil, CorrectOutput{}, err
	}

	out := CorrectOutput{Ref: ref.String()}

	if in.Reason != "" {
		historyRef, err := s.recordReason(ref.String(), in.Guidance, in.Reason)
		if err != nil {
			return nil, CorrectOutput{}, err
		}
		out.History = historyRef
	}

	return nil, out, nil
}

// recordReason appends the reasoning behind a correction to the history.
func (s *Server) recordReason(target, guidance, reason string) (string, error) {
	ref, path, err := s.writable("topic:corrections")
	if err != nil {
		return "", err
	}

	note, err := s.vault.Read(path)
	if err != nil {
		return "", err
	}

	today := schema.Today()
	note.Append(
		fmt.Sprintf("## %s — %s", today, firstLine(guidance)),
		fmt.Sprintf("%s\n\nRule recorded in `%s`.", reason, target),
	)
	note.Touch(today)

	if err := s.vault.Write(note); err != nil {
		return "", err
	}

	return ref.String(), nil
}

// firstLine returns a short title drawn from the guidance: its first sentence,
// truncated on a rune boundary.
func firstLine(s string) string {
	const limit = 60

	if i := strings.IndexAny(s, ".\n"); i > 0 {
		s = s[:i]
	}
	if runes := []rune(s); len(runes) > limit {
		s = string(runes[:limit])
	}
	return strings.TrimSpace(s)
}

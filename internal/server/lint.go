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
	Ref   string `json:"ref,omitempty" jsonschema:"a single note to check; omit to check whole vaults"`
	Vault string `json:"vault,omitempty" jsonschema:"restrict the check to one vault: a team slug, or personal for your own; omit to check every vault you can reach"`
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

// lint validates one note, one vault, or every vault the session can reach.
//
// Findings from a team vault carry the team in their refs, so a finding can be
// fed straight back into note_read or note_update.
func (s *Server) lint(ctx context.Context, _ *sdk.CallToolRequest, in LintInput) (*sdk.CallToolResult, LintOutput, error) {
	var (
		targets []*scoped
		paths   []string
	)
	if in.Ref != "" {
		sc, _, path, err := s.resolveRef(ctx, in.Ref, in.Vault)
		if err != nil {
			return nil, LintOutput{}, err
		}
		targets, paths = []*scoped{sc}, []string{path}
	} else {
		var err error
		if targets, err = s.vaultsFor(ctx, in.Vault); err != nil {
			return nil, LintOutput{}, err
		}
	}

	out := LintOutput{Findings: []Finding{}}

	for _, sc := range targets {
		findings, err := sc.vault.Lint(paths...)
		if err != nil {
			return nil, LintOutput{}, err
		}

		for _, f := range findings {
			out.Findings = append(out.Findings, Finding{
				Ref:      sc.qualify(sc.canonical(f.Path)),
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
	}

	out.Clean = len(out.Findings) == 0

	return nil, out, nil
}

// CorrectInput records a correction from the user.
type CorrectInput struct {
	Target string `json:"target" jsonschema:"where the rule belongs: contract, topic:<name>, or standard:<name>. A rule goes to your personal vault unless the target names a team, as in @platform:standard:testing or @platform:contract"`

	Guidance string `json:"guidance" jsonschema:"the rule, stated as an instruction to follow in future"`

	Reason string `json:"reason,omitempty" jsonschema:"what prompted the correction; recorded in the correction history, not in the rule"`

	Section string `json:"section,omitempty" jsonschema:"heading to file the rule under; defaults to Recorded Corrections. Name the section the rule actually belongs to when you know it"`
}

// CorrectionsSection is where a recorded correction lands when the caller does
// not name a section.
//
// A correction has to go somewhere predictable. Appending it to the end of the
// document files it under whatever heading is last, which is how a rule about
// repository layout ends up reading as part of the session checklist. A
// dedicated heading is honest about what it holds: rules that are in force but
// have not yet been worked into the prose around them.
const CorrectionsSection = "Recorded Corrections"

// CorrectOutput reports where the correction went.
type CorrectOutput struct {
	Ref     string `json:"ref"`
	History string `json:"history_ref,omitempty"`

	GitNotice
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

	// The target says which vault the rule belongs to. Unscoped is the
	// caller's own, which is where a correction from a member of a team goes
	// by default: a team's rules are the team's to change, and the policy
	// below is what says who may.
	sc, rest, err := s.route(ctx, in.Target)
	if err != nil {
		return nil, CorrectOutput{}, fmt.Errorf("correction target %q: %w", in.Target, err)
	}
	if rest == "" || rest == "contract" {
		rest = "topic:operating"
	}

	if sc.slug != "" && s.CorrectPolicy != nil {
		if err := s.CorrectPolicy(ctx, sc.slug); err != nil {
			return nil, CorrectOutput{}, fmt.Errorf(
				"recording a correction in team %s is not permitted: %w", sc.slug, err)
		}
	}

	unlock := sc.beginWrite()
	defer unlock()

	ref, path, err := sc.writable(rest)
	if err != nil {
		return nil, CorrectOutput{}, fmt.Errorf("correction target %q: %w", in.Target, err)
	}

	note, err := sc.vault.Read(path)
	if err != nil {
		return nil, CorrectOutput{}, err
	}

	section := strings.TrimSpace(in.Section)
	if section == "" {
		section = CorrectionsSection
	}
	if err := note.AppendToSection(section, fmt.Sprintf("- %s", in.Guidance)); err != nil {
		return nil, CorrectOutput{}, fmt.Errorf("record correction in %s: %w", ref, err)
	}
	note.Touch(schema.Today())

	if err := sc.write(note); err != nil {
		return nil, CorrectOutput{}, err
	}

	out := CorrectOutput{Ref: sc.qualify(ref)}

	if in.Reason != "" {
		historyRef, err := sc.recordReason(ref.String(), in.Guidance, in.Reason)
		if err != nil {
			return nil, CorrectOutput{}, err
		}
		out.History = historyRef
	}

	// The guidance is the reason: a correction's why is the rule it records,
	// and CorrectInput's own Reason is what prompted it, which belongs in the
	// history rather than in the commit.
	out.GitWarning = sc.record("gandalf: correct "+ref.String(), in.Guidance)
	return nil, out, nil
}

// recordReason appends the reasoning behind a correction to the history.
func (c *scoped) recordReason(target, guidance, reason string) (string, error) {
	// A team's vault is not required to keep a correction history. The rule
	// itself is already in place by the time this runs, and failing the whole
	// call over a missing log would leave a recorded rule reported as an
	// error, so the reason is simply not kept there.
	if c.slug != "" {
		if _, path, err := c.resolve("topic:corrections"); err != nil || !c.vault.Exists(path) {
			return "", nil
		}
	}

	ref, path, err := c.writable("topic:corrections")
	if err != nil {
		return "", err
	}

	note, err := c.vault.Read(path)
	if err != nil {
		return "", err
	}

	today := schema.Today()
	note.Append(
		fmt.Sprintf("## %s — %s", today, firstLine(guidance)),
		fmt.Sprintf("%s\n\nRule recorded in `%s`.", reason, target),
	)
	note.Touch(today)

	if err := c.write(note); err != nil {
		return "", err
	}

	return c.qualify(ref), nil
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

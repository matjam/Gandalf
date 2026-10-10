package server

import (
	"strings"
	"testing"
)

// Each vault has its own repository, so each has its own health: a broken team
// repository must reach the writer who changed that team's notes, in the
// result they are reading, and must say nothing about the vaults that work.

// TestTeamWritesWarnWhenTheTeamRepositoryIsBroken: every writing tool, aimed at
// a team vault, carries git_warning when that vault's repository is broken.
func TestTeamWritesWarnWhenTheTeamRepositoryIsBroken(t *testing.T) {
	tests := []struct {
		tool string

		// setup runs while git still works and returns the arguments for the
		// call that follows the breakage.
		setup func(m *multi) any
	}{
		{"session_start", func(m *multi) any {
			return SessionStartInput{Title: "A broken morning", Vault: "platform"}
		}},
		{"note_new", func(m *multi) any {
			return NoteNewInput{Kind: "standard", Title: "Reviews", Content: "Read the diff.", Vault: "platform"}
		}},
		{"note_append", func(m *multi) any {
			m.call("note_new", NoteNewInput{Kind: "standard", Title: "Testing", Content: "Table tests.", Vault: "platform"}, nil)
			return NoteAppendInput{Ref: "@platform:standard:testing", Content: "More."}
		}},
		{"note_update", func(m *multi) any {
			m.call("note_new", NoteNewInput{Kind: "standard", Title: "Testing", Content: "Table tests.", Vault: "platform"}, nil)
			return NoteUpdateInput{Ref: "@platform:standard:testing", AddTags: []string{"extra"}}
		}},
		{"note_replace", func(m *multi) any {
			m.call("note_new", NoteNewInput{Kind: "standard", Title: "Testing", Content: "## Rules\n\nTable tests.\n", Vault: "platform"}, nil)
			return NoteReplaceInput{Ref: "@platform:standard:testing", Section: "Rules", Content: "Subtests."}
		}},
		{"note_delete", func(m *multi) any {
			m.call("note_new", NoteNewInput{Kind: "standard", Title: "Doomed", Content: "Gone.", Vault: "platform"}, nil)
			return NoteDeleteInput{Ref: "@platform:standard:doomed"}
		}},
		{"category_create", func(m *multi) any {
			return CategoryCreateInput{
				Name: "incident", Plural: "incidents", Rule: "dated", Folder: "Incidents",
				Description: "Something broke and why.", Vault: "platform",
			}
		}},
		{"topic_new", func(m *multi) any {
			return TopicNewInput{Title: "Review", When: "Reviewing a change.", Content: "Read the diff.", Vault: "platform"}
		}},
		{"correct", func(m *multi) any {
			return CorrectInput{Target: "@platform:contract", Guidance: "Check the repository first."}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			m := newMulti(t, multiOptions{})
			args := tt.setup(m)

			breakGitAt(t, m.platform.vault.Root())

			warning, ok := gitWarningOf(m.harness, tt.tool, args)
			if !ok {
				t.Fatalf("%s returned no git_warning although the team repository is broken", tt.tool)
			}
			for _, want := range []string{
				"saved to the vault but NOT recorded in git",
				"broken since",
				"NOT being committed or synced",
			} {
				if !strings.Contains(warning, want) {
					t.Errorf("git_warning = %q, want it to say %q", warning, want)
				}
			}
		})
	}
}

// TestABrokenTeamRepositoryDoesNotWarnAboutOtherVaults: the warning belongs to
// the vault that was written, in both directions.
func TestABrokenTeamRepositoryDoesNotWarnAboutOtherVaults(t *testing.T) {
	m := newMulti(t, multiOptions{})
	repair := breakGitAt(t, m.platform.vault.Root())

	t.Run("personal and other team writes are quiet", func(t *testing.T) {
		if warning, ok := gitWarningOf(m.harness, "session_start", SessionStartInput{Title: "Mine"}); ok {
			t.Errorf("a personal write warned about the team's repository: %q", warning)
		}
		if warning, ok := gitWarningOf(m.harness, "note_new",
			NoteNewInput{Kind: "standard", Title: "Other", Vault: "data"}); ok {
			t.Errorf("a write to another team warned: %q", warning)
		}
	})

	t.Run("the team write warns, and a repair clears it", func(t *testing.T) {
		args := NoteNewInput{Kind: "standard", Title: "Broken", Content: "Words.", Vault: "platform"}
		if _, ok := gitWarningOf(m.harness, "note_new", args); !ok {
			t.Fatal("a write to the broken team vault did not warn")
		}

		repair()
		args.Title = "Repaired"
		if warning, ok := gitWarningOf(m.harness, "note_new", args); ok {
			t.Errorf("a write after the repair still warned: %q", warning)
		}
	})

	t.Run("the note was still saved", func(t *testing.T) {
		if got := diskIn(t, m.platform, "standard:broken"); !strings.Contains(got, "Words.") {
			t.Errorf("the note written while git was broken is not on disk: %q", got)
		}
	})
}

// TestBootReportsABrokenTeamRepository: boot says, for each vault, whether git
// is working there; the personal vault comes first, and a team's report is
// scoped to that team.
func TestBootReportsABrokenTeamRepository(t *testing.T) {
	t.Run("only a team is broken", func(t *testing.T) {
		m := newMulti(t, multiOptions{})
		breakGitAt(t, m.platform.vault.Root())

		var out BootOutput
		m.call("boot", BootInput{}, &out)

		if out.GitWarning != "" {
			t.Errorf("the personal vault's git_warning = %q, want none", out.GitWarning)
		}
		var platform, data *TeamBoot
		for i := range out.Teams {
			switch out.Teams[i].Slug {
			case "platform":
				platform = &out.Teams[i]
			case "data":
				data = &out.Teams[i]
			}
		}
		if platform == nil || data == nil {
			t.Fatalf("teams = %+v", out.Teams)
		}
		if !strings.Contains(platform.GitWarning, "broken since") {
			t.Errorf("platform git_warning = %q, want it to say git is broken", platform.GitWarning)
		}
		if data.GitWarning != "" {
			t.Errorf("data git_warning = %q, want none", data.GitWarning)
		}

		var notice string
		for _, n := range out.Notices {
			if strings.HasPrefix(n, "GIT IS NOT WORKING") {
				if notice != "" {
					t.Errorf("more than one git notice: %q and %q", notice, n)
				}
				notice = n
			}
		}
		if !strings.Contains(notice, "team platform") || !strings.Contains(notice, platform.GitWarning) {
			t.Errorf("notice = %q, want it to name the team and say what failed", notice)
		}
	})

	t.Run("the personal vault is reported first", func(t *testing.T) {
		m := newMulti(t, multiOptions{})
		breakGitAt(t, m.data.vault.Root())
		breakGitAt(t, m.personal.vault.Root())

		var out BootOutput
		m.call("boot", BootInput{}, &out)

		if out.GitWarning == "" {
			t.Error("the personal vault's broken repository is not reported")
		}
		var git []string
		for _, n := range out.Notices {
			if strings.HasPrefix(n, "GIT IS NOT WORKING") {
				git = append(git, n)
			}
		}
		if len(git) != 2 {
			t.Fatalf("git notices = %q, want one for the personal vault and one for the team", git)
		}
		if strings.Contains(git[0], "team") || !strings.Contains(git[1], "team data") {
			t.Errorf("notices out of order or unscoped: %q", git)
		}
	})

	t.Run("a healthy install says nothing", func(t *testing.T) {
		m := newMulti(t, multiOptions{})

		var out BootOutput
		m.call("boot", BootInput{}, &out)

		if out.GitWarning != "" {
			t.Errorf("git_warning = %q", out.GitWarning)
		}
		for _, team := range out.Teams {
			if team.GitWarning != "" {
				t.Errorf("%s git_warning = %q", team.Slug, team.GitWarning)
			}
		}
		for _, n := range out.Notices {
			if strings.Contains(n, "GIT IS NOT WORKING") {
				t.Errorf("notice %q", n)
			}
		}
	})
}

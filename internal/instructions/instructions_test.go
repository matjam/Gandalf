package instructions

import "testing"

// TestHashBodyIgnoresLineEndings keeps a checkout's newline convention out of
// the seed fingerprint.
//
// go:embed compiles the shipped documents in with whatever line endings the
// checkout used. Writing one and reading it back normalises CRLF to LF, so a
// fingerprint over the raw bytes made every document on a CRLF checkout report
// as modified — permanently, and on Windows only.
func TestHashBodyIgnoresLineEndings(t *testing.T) {
	lf := "# Title\n\nA line.\nAnother line.\n"
	crlf := "# Title\r\n\r\nA line.\r\nAnother line.\r\n"

	if HashBody(lf) != HashBody(crlf) {
		t.Errorf("HashBody differs by line ending: %s vs %s", HashBody(lf), HashBody(crlf))
	}

	// It must still notice an actual change.
	if HashBody(lf) == HashBody(lf+"\nSomething new.\n") {
		t.Error("HashBody ignored a real edit")
	}
}

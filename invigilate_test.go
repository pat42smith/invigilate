// Copyright 2023 Patrick Smith
// Use of this source code is subject to the MIT-style license in the LICENSE file.

package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pat42smith/gotest"
	"github.com/pat42smith/or"
)

// Build the executable and use it to run the real tests as subtests.
func TestAll(t *testing.T) {
	tmp := t.TempDir()
	ex := filepath.Join(tmp, "invigilate")
	gotest.Command("go", "build", "-o", ex).Run(t, "")

	t.Run("Defaults", func (t2 *testing.T) { Defaults(t2, ex) })
	t.Run("Time Limit", func (t2 *testing.T) { Time(t2, ex) })
	t.Run("Extension", func (t2 *testing.T) { Extension(t2, ex) })
	t.Run("Comment", func (t2 *testing.T) { Comment(t2, ex) })
	t.Run("Verbose", func (t2 *testing.T) { Verbose(t2, ex) })
	t.Run("Help", func (t2 *testing.T) { Help(t2, ex) })
	t.Run("Error", func (t2 *testing.T) { Error(t2, ex) })
	t.Run("Testee", func (t2 *testing.T) { Testee(t2, ex) })
}

// Test some invocations with default arguments.
func Defaults(t *testing.T, invig string) {
	gotest.Command(invig, "/bin/sh", "--", "testdata/null", "testdata/normal").Run(t, "")

	mustFail := func(testcase, msg string) {
		cmd := gotest.Command(invig, "/bin/sh", "--", testcase)
		cmd.WantStderr(testcase + ": " + msg + "\n1 failed tests\n")
		cmd.WantCode(1)
		cmd.Run(t, "")
	}

	mustFail("testdata/fail/badoutput.test", `incorrect test output
expected: right
  actual: wrong`)

	// This test should have been skipped above; it must fail.
	mustFail("testdata/normal/skip.sh", `extra output: This test case should not be run`)

	mustFail("testdata/fail/baderror.test", `incorrect test error output
expected: Nonsense!
  actual: Blimey!`)

	mustFail("testdata/fail/badorder.test", `time limit exceeded`)

	mustFail("testdata/fail/toolong.test", `time limit exceeded`)

	mustFail("testdata/fail/missingoutput.test", `incomplete test output
expected: beta
  actual: `)

	mustFail("testdata/fail/missingerror.test", `incomplete test error output
expected: Oops!
  actual: `)

	mustFail("testdata/fail/halflineoutput.test", `incomplete test output
expected: Bobolink
  actual: Bob`)

	mustFail("testdata/fail/halflineerror.test", `incomplete test error output
expected: I'm riding a roller coaster!
  actual: I'm riding a roll`)

	mustFail("testdata/fail/extraoutput.test", `extra output: beta`)

	mustFail("testdata/fail/extraerror.test", `extra error output: Yes, it is!`)

	cmd := gotest.Command(invig, "/bin/sh", "--",
		"testdata/normal/hello.test",
		"testdata/fail/baderror.test",
		"testdata/normal/oops.test",
		"testdata/fail/halflineerror.test",
		"testdata/normal/split.test")
		cmd.WantStderr(`testdata/fail/baderror.test: incorrect test error output
expected: Nonsense!
  actual: Blimey!
testdata/fail/halflineerror.test: incomplete test error output
expected: I'm riding a roller coaster!
  actual: I'm riding a roll
2 failed tests
`)
	cmd.WantCode(1)
	cmd.Run(t, "")

	cmd = gotest.Command(invig, "/bin/sh", "--", "testdata/mix")
	cmd.WantStderr(`testdata/mix/bumblebee.test: incorrect test output
expected: bumblebee
  actual: hornet
testdata/mix/dingo.test: incorrect test output
expected: dingo
  actual: fox
testdata/mix/elk.test: incorrect test output
expected: elk
  actual: moose
3 failed tests
`)
	cmd.WantCode(1)
	cmd.Run(t, "")
}

// Check the time limit option
func Time(t *testing.T, invig string) {
	gotest.Command(invig, "-t", ".7s", "/bin/sh", "--", "testdata/halfsecond.test").Run(t, "")

	cmd := gotest.Command(invig, "-t", ".3s", "/bin/sh", "--", "testdata/halfsecond.test")
	cmd.WantStderr(`testdata/halfsecond.test: time limit exceeded
1 failed tests
`)
	cmd.WantCode(1)
	cmd.Run(t, "")
}

// Check the filename extension option
func Extension(t *testing.T, invig string) {
	cmd := gotest.Command(invig, "-e", ".sh", "/bin/sh", "--", "testdata/normal", "testdata/fail")
	cmd.WantStderr(`testdata/normal/skip.sh: extra output: This test case should not be run
1 failed tests
`)
	cmd.WantCode(1)
	cmd.Run(t, "")
}

// Check non-standard comment delimiters
func Comment(t *testing.T, invig string) {
	gotest.Command(invig, "-c", "###", "/bin/sh", "--", "testdata/comment.test").Run(t, "")

	cmd := gotest.Command(invig, "-c", " #", "/bin/sh", "--", "testdata/comment.test")
	cmd.WantStderr(`testdata/comment.test: incorrect test error output
expected: error
  actual: oops
1 failed tests
`)
	cmd.WantCode(1)
	cmd.Run(t, "")
}

// Check verbose output
func Verbose(t *testing.T, invig string) {
	cmd := gotest.Command(invig, "-v", "/bin/sh", "--", "testdata/normal")
	cmd.WantStdout(`
testdata/normal/1second.test
>Boo!

testdata/normal/extraread.test
>Say something
!no input

testdata/normal/hello.test
>What is your name?
<Alice
>Hello, Alice

testdata/normal/noEOFerror.test
!Something's missing!

testdata/normal/noEOFoutput.test
>Boo

testdata/normal/nonsense.test
<lavish
>No McTavish
>Was ever lavish
<Seuss
>You're wrong as the deuce
>And you shouldn't rejoice
>If you're calling him Seuss.
>He pronounces it Soice (or Zoice)
<Jabberwocky
>Twas brillig, and the slithy toves
>Did gyre and gimble in the wabe;
>All mimsy were the borogoves,
>And the mome raths outgrabe.
<Aosta
>There was an Old Man of Aosta
>Who possessed a large Cow, but he lost here;
>But they said, "Don't you see she has run up a tree,
>You invidious Old Man of Aosta?"
<done

testdata/normal/oops.test
!Oops

testdata/normal/split.test
>Hello, world!

testdata/normal/world.test
>Hello, world!

All tests passed.
`)
	cmd.Run(t, "")

	os.Setenv("INVIGILATE", invig)
	gotest.Command(invig, "/bin/sh", "--", "testdata/verbosemix.sh").Run(t, "")
}

// Check help output
func Help(t *testing.T, invig string) {
	cmd := gotest.Command(invig, "-h")
	cmd.CheckStderr(func(actual string) bool {
		// Since the help text might change often, we'll just do a few simple checks.
		return len(actual) > 500 &&
			strings.Contains(actual, "invigilate") &&
			strings.Contains(actual, "-e")
	})
	cmd.WantCode(0)
	cmd.Run(t, "")
}

// Check reporting of errors
func Error(t *testing.T, invig string) {
	tmp := t.TempDir()
	mix := filepath.Join(tmp, "mix")
	bee := filepath.Join(mix, "bumblebee.test")
	gotest.Command("/bin/cp", "-a", "testdata/mix", mix).Run(t, "")
	or.Fatal0(os.Chmod(bee, 0))

	cmd := gotest.Command(invig, "/bin/sh", "--", bee)
	cmd.CheckStderr(func(actual string) bool {
		return strings.Contains(actual, "permission denied") &&
			strings.HasSuffix(actual, "0 failed tests; 1 other errors\n")
	})
	cmd.WantCode(1)
	cmd.Run(t, "")

	cmd = gotest.Command(invig, "/bin/sh", "--", mix)
	cmd.CheckStderr(func(actual string) bool {
		return strings.Contains(actual, "permission denied") &&
			strings.HasSuffix(actual, "2 failed tests; 1 other errors\n")
	})
	cmd.WantCode(1)
	cmd.Run(t, "")
}

// Test something other than /bin/sh
func Testee(t *testing.T, invig string) {
	gotest.Command(invig, "/usr/bin/awk", "-f", "--", "testdata/sum.test").Run(t, "")
}

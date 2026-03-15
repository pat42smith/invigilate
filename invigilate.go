// Copyright 2024-2026 Patrick Smith
// Use of this source code is subject to the MIT-style license in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Function usage prints a usage message to writer.
// If writer is nil, os.Stderr is assumed.
func usage(writer io.Writer) {
	if writer == nil {
		writer = os.Stderr
	}

	fmt.Fprint(writer, `
Usage: invigilate [options] program -- files

Program invigilate runs a number of test cases against a single program.

The arguments between the last option and the "--" describe the program
to be tested.

The arguments after "--" list files containing test cases. If one of these
arguments refers to a directory, the directory will be searched (recursively)
for regular files with the extension given by the -e option; these will be
used as test cases. Test case files listed directly in the command line do not
need to end with the extension given with -e.

The program being tested is run once for each test case. The command line
consists of the "program" part of the invigilate arguments, followed by one
additional argument, the path to the file containing the test case.

The expected results of a test case are described in comments embedded in the
test file. A line beginning with "#>" means that the remainder of the line
should appear on standard output; "#!", that the remainder should appear on the
standard error output; and "#<", that the remainder should be supplied to
standard input. The newline character terminating each such line is included in
the input or expected output. The -c option may be used to specify another
comment delimiter instead of "#", but the delimiter must always appear at the
beginning of a line.

Invigilate processes #>, #!, and #< directives in the order in which they
appear in the test file. So a test case such as (using sh):

   read value
   echo prompt
   #>prompt
   #<something

will hang until the time limit runs out, as both invigilate and the test case
will be trying to read from each other.

Note that invigilate cannot guarantee the relative ordering of output to
standard output and standard error. A test such as:

   echo one
   echo two >&2
   #>two
   #!one

will probably succeed. In some cases, however, such a test might block until
the time limit expires. This is more likely when there is a large amount
of output.

Invigilate will close the standard input for the test case after it processes
the last #< line. If there are no #< lines, standard input will be closed
immediately after the test case is started. This allows a test case to use
EOF on standard input as an indication to terminate.

In addition to input and output, invigilate checks the exit code from the test
process. If no output on standard error is expected, then a 0 exit code is
expected. Otherwise, any non-0 exit code is accepted.

If the -a option is given and a test file contains a line beginning with "#-",
then the rest of the line is divided into words separated by whitespace. These
words are appended to the command line for that test, before the test filename.
Multiple #- lines may be used; they are appended in the order they occur.
Shell-style quoting is not supported.

When the time limit for a test case runs out, invigilate tries to kill the test
process and proceed with other test cases. If the test case has created a
long-lived child process, the child process might be left running even after
invigilate has finished running the full test suite.

Options:
   -a            allow giving extra arguments with #-
   -c string     comment delimiter for expected input and output (default #)
   -e string     test case files have this extension (default .test)
   -h            print this help information
   -t duration   time limit for individual test cases (default 2s = 2 seconds)
                 (as accepted by https://pkg.go.dev/time#ParseDuration)
   -v            show verbose output
`)
}

// allowExtraArgs indicates whether to process #- lines
var allowExtraArgs = false

// verbose indicates whether verbose output was requested
var verbose = false

// Within test case files, lines displaying test input or desired output
// must begin with comment. Default: "#".
var comment = "#"

// When searching directories for test case files, only files whose names
// end with extension are considered. Default: ".test".
var extension = ".test"

// failCount counts the number of failed tests.
var failCount = 0

// errorCount counts the number of errors that are not considered test failures.
var errorCount = 0

// limit is the time within which a single test must complete
var limit = 2 * time.Second

// Test represents one test case file to be executed or reported as an error.
type Test struct {
	// The path to the file
	path string

	// The content of the file; "" whenever err is not nil.
	content string

	// Any error that occurred processing the file
	err error
}

func main() {
	log.SetFlags(0)

	// Process command line flags.
	// We don't use the standard flag package because it wants to ignore --
	// immediately after the options. That gets confusing, since we use --
	// as a separator.

	var args []string
	if len(os.Args) > 0 {
		args = os.Args[1:]
	}

	for len(args) > 0 && len(args[0]) > 0 && args[0][0] == '-' && args[0] != "--" {
		arg0 := args[0]
		args = args[1:]
		switch arg0 {
		case "-c", "-e", "-t":
			if len(args) == 0 {
				usage(nil)
				log.Println()
				log.Fatalf("missing value for %s", arg0)
			}
			switch arg0 {
			case "-c":
				comment = args[0]
			case "-e":
				extension = args[0]
			case "-t":
				if d, e := time.ParseDuration(args[0]); e != nil {
					log.Fatal(e)
				} else {
					limit = d
				}
			}
			args = args[1:]
		case "-a":
			allowExtraArgs = true
		case "-v":
			verbose = true
		case "-h", "--h", "-help", "--help":
			usage(os.Stdout)
			return
		default:
			usage(nil)
			log.Println()
			log.Fatalf("unknown option %s", arg0)
		}
	}

	dashes := slices.Index(args, "--")
	if dashes < 0 {
		usage(nil)
		log.Println()
		log.Fatal("missing -- in arguments")
	}

	// Allocate a spot for a test name in the program's command line
	// Note that if we ever switch to multiple worker goroutines for running tests,
	// this will have to change so those workers don't step on each other's test names.
	program := make([]string, dashes, dashes+1)
	copy(program, args[:dashes])

	roots := args[dashes+1:]

	if len(program) == 0 {
		usage(nil)
		log.Println()
		log.Fatal("missing program")
	} else if len(roots) == 0 {
		usage(nil)
		log.Println()
		log.Fatal("missing test cases")
	}

	// Find the executable for the program.
	// If the program doesn't exist, we want to complain just once,
	// not once per test. Also, we save a tiny bit of time by
	// not repeating the search for each test.
	if path, e := exec.LookPath(program[0]); e != nil {
		log.Fatalf("%s: %s", program[0], e)
	} else {
		program[0] = path
	}

	ch := make(chan Test, 10)
	go findTests(roots, ch)

	for t := range ch {
		if t.err != nil {
			log.Print(t.err)
			errorCount++
		} else {
			runTest(t, program)
		}
	}

	if errorCount > 0 || failCount > 0 {
		emsg := ""
		if errorCount > 0 {
			emsg = fmt.Sprintf("; %d other errors", errorCount)
		}
		log.Fatalf("%d failed tests%s", failCount, emsg)
	}

	if verbose {
		fmt.Println()
		fmt.Println("All tests passed.")
	}
}

// findTests finds the test cases to be executed
func findTests(roots []string, ch chan<- Test) {
	for _, r := range roots {
		info, e := os.Lstat(r)
		if e != nil {
			ch <- Test{r, "", e}
			continue
		}
		if info.Mode().IsRegular() {
			reportTest(r, ch)
		} else if !info.IsDir() {
			ch <- Test{r, "", fmt.Errorf("%s is neither a regular file nor a directory", r)}
		} else {
			filepath.WalkDir(r, func(path string, de fs.DirEntry, err error) error {
				if err != nil {
					ch <- Test{path, "", err}
				} else if de.Type().IsRegular() {
					base := filepath.Base(path)
					if strings.HasSuffix(base, extension) {
						reportTest(path, ch)
					}
				}
				return nil
			})
		}
	}
	close(ch)
}

// reportTest lists one test case that should be executed
func reportTest(path string, ch chan<- Test) {
	content, e := os.ReadFile(path)
	if e != nil {
		ch <- Test{path, "", e}
		return
	}
	ch <- Test{path, string(content), nil}
}

// attachPipes creates pipes for a command's standard IO
//
// If an error occurs, the caller will be responsible for closing
// the pipe file descriptors. See the call site below.
func attachPipes(cmd *exec.Cmd, test string) (iPipe io.WriteCloser, oPipe, ePipe io.ReadCloser, e error) {
	if iPipe, e = cmd.StdinPipe(); e != nil {
		log.Printf("error opening input pipe for %s: %s", test, e)
		return
	}
	if oPipe, e = cmd.StdoutPipe(); e != nil {
		log.Printf("error opening output pipe for %s: %s", test, e)
		return
	}
	if ePipe, e = cmd.StderrPipe(); e != nil {
		log.Printf("error opening error output pipe for %s: %s", test, e)
		return
	}

	return
}

var buf = make([]byte, 65536)

// expect tries to match one line of expected output against the output actually
// received from the test program. The parameters are:
//
// pipe: Our end of the pipe to which the test program is writing.
// what: A description of the pipe from the test program's point of view.
// .     E.g. "standard output"
// test: The path to the test file.
// want: The line we expect to see on the output. Usually, but not always,
// .     includes a trailing newline, which must be matched.
// got:  Contains text previously received but not yet matched.
// fail: To be called on any sort of error.
//
// The result indicates whether we successfully matched the expected output.
func expect(pipe io.ReadCloser, what, test, want string, got *string, fail func(string, error)) bool {
	for same, done := 0, false; ; {
		for same < len(want) && same < len(*got) {
			if want[same] == (*got)[same] {
				same++
			} else {
				have := *got
				if n := strings.IndexByte(have, '\n'); n >= 0 {
					have = have[:n+1]
				}
				log.Printf("%s: incorrect %s", test, what)
				log.Printf("expected: %s", want)
				log.Printf("  actual: %s", have)
				fail("", nil)
				return false
			}
		}
		if same >= len(want) {
			*got = (*got)[len(want):]
			return true
		}
		if done {
			log.Printf("%s: incomplete %s", test, what)
			log.Printf("expected: %s", want)
			log.Printf("  actual: %s", *got)
			fail("", nil)
			return false
		}
		// TODO: Speed this up; in principle, this is O(N*N). But the strings in question
		// will generally be short, and we're not likely to be reading data 1 byte at a time.
		n, e := pipe.Read(buf)
		*got += string(buf[:n])
		if e == io.EOF {
			done = true
		} else if e != nil {
			fail("reading "+what, e)
			return false
		}
	}
}

// runTest runs a single test case
func runTest(t Test, program []string) {
	lines := strings.SplitAfter(t.content, "\n")
	reads := 0
	readPrefix := comment + "<"
	allowPrefix := comment + "-"
	for _, line := range lines {
		if strings.HasPrefix(line, readPrefix) {
			reads++
		} else if allowExtraArgs && strings.HasPrefix(line, allowPrefix) {
			program = append(program, strings.Fields(line[len(allowPrefix):])...)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	program = append(program, t.path)
	cmd := exec.CommandContext(ctx, program[0], program[1:]...)

	iPipe, oPipe, ePipe, e := attachPipes(cmd, t.path)
	if e != nil {
		// This is extremely unlikely. But if and when it happens, we won't start
		// the command. In this case, https://github.com/golang/go/issues/58369
		// suggests cancelling the context and then calling Start, which should
		// release the pipe file descriptors without starting the command.
		errorCount++
		cancel()
		cmd.Start()
		return
	}

	// If we cancel the command (e.g. because of a timeout), we need to close the pipes,
	// so the reads and writes below will terminate. This guards against the case
	// where a test process spawns a child that still has its end of the pipes open;
	// killing the test process doesn't kill the child. By closing our end of the pipes,
	// we can proceed to other test cases, no matter how long the child hangs around.
	oldCmdCancel := cmd.Cancel // Should kill the test process
	cmd.Cancel = func() error {
		oldCmdCancel()
		iPipe.Close()
		oPipe.Close()
		ePipe.Close()
		return nil
	}

	// From here on, cmd.Start and cmd.Wait will close the pipes for us.
	// We do close our end of each pipe when we are finished with, but that
	// is not strictly necessary. We must be careful to never use a pipe after
	// we have closed it; that might cause faile to emit a spurious report
	// of time limit exceeded.
	//
	// Also, any errors occurring after this point will be considered test failures.

	if verbose {
		fmt.Println()
		fmt.Println(t.path)
	}

	if e = cmd.Start(); e != nil {
		log.Printf("%s: %s\n", t.path, e)
		failCount++
		return
	}

	fail := func() {
		failCount++
		cancel()
	}

	faile := func(msg string, e error) {
		if errors.Is(e, os.ErrClosed) {
			log.Printf("%s: time limit exceeded", t.path)
		} else if e != nil {
			log.Printf("%s: %s: %s", t.path, msg, e)
		}
		fail()
	}

	var ogot, egot string
	erred := false
	for _, line := range lines {
		if reads == 0 {
			// Close stdin as soon as there is nothing more to send.
			if e := iPipe.Close(); e != nil {
				faile("closing test input", e)
				return
			}
			reads = -1
		}
		if !strings.HasPrefix(line, comment) || len(line) < len(comment)+2 {
			continue
		}
		line = line[len(comment):]
		if verbose {
			switch line[0] {
			case '<', '>', '!':
				fmt.Print(line)
				if line[len(line)-1] != '\n' {
					fmt.Println()
				}
			}
		}

		data := line[1:]
		switch line[0] {
		case '<':
			reads--
			for k := 0; k < len(data); {
				if n, e := io.WriteString(iPipe, data[k:]); e != nil {
					faile("writing to test input", e)
					return
				} else {
					k += n
				}
			}
		case '>':
			if !expect(oPipe, "test output", t.path, data, &ogot, faile) {
				return
			}
		case '!':
			erred = true
			if !expect(ePipe, "test error output", t.path, data, &egot, faile) {
				return
			}
		case '-':
			// Handled above
		}
	}

	if reads > 0 {
		panic("bug")
	} else if reads == 0 {
		// Should only happen for an empty test case.
		if e := iPipe.Close(); e != nil {
			faile("closing test input", e)
			return
		}
		reads = -1
	}

	// Check for extra output, then close the pipe.
	endOutput := func(pipe io.ReadCloser, which string, got string) bool {
		if got == "" {
			// Don't try to read too much output. If the test case spews thousands
			// of bytes of messages, we don't want to include them all in our error message.
			n, e := pipe.Read(buf[:64])
			got = string(buf[:n])
			if n >= 64 {
				got += "..."
			}
			if e != nil && !errors.Is(e, io.EOF) {
				faile("error reading "+which, e)
				return false
			}
		}

		if got != "" {
			log.Printf("%s: extra output on %s: %s", t.path, which, got)
			fail()
			return false
		}

		if e := pipe.Close(); e != nil {
			faile("closing test "+which, e)
			return false
		}

		return true
	}

	if !endOutput(oPipe, "stdout", ogot) {
		return
	}
	if !endOutput(ePipe, "stderr", egot) {
		return
	}

	code := 0
	if e := cmd.Wait(); e != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			log.Printf("%s: time limit exceeded", t.path)
			failCount++
			return
		}

		if ee, ok := e.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			if !errors.Is(ctx.Err(), context.Canceled) {
				log.Printf("%s: %s", t.path, e)
			}
			failCount++
			return
		}
	}

	if erred {
		if code == 0 {
			log.Printf("%s: produced error output but exit code was 0", t.path)
			failCount++
			return
		}
	} else {
		if code != 0 {
			log.Printf("%s: exit code %d", t.path, code)
			failCount++
			return
		}
	}
}

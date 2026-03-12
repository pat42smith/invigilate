// Copyright 2024-2026 Patrick Smith
// Use of this source code is subject to the MIT-style license in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Function usage prints a usage message to stdout or stderr.
func usage() {
	fmt.Fprint(flag.CommandLine.Output(), `
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

will probably succeed.

Invigilate will close the standard input for the test case after it processes
the last #< line. If there are no #< lines, standard input will be closed
immediately after the test case is started. This allows a test case to use
EOF on standard input as an indication to terminate.

In addition to input and output, invigilate checks the exit code from the test
process. If no output on standard error is expected, then a 0 exit code is
expected. Otherwise, any non-0 exit code is accepted.

When the time limit for a test case runs out, invigilate tries to kill the test
process and proceed with other test cases. If the test case has created a
long-lived child process, the child process might be left running even after
invigilate has finished running the full test suite.

Options:

`)

	flag.PrintDefaults()
}

// verbose indicates whether verbose output was requested
var verbose bool

// Within test case files, lines displaying test input or desired output
// must begin with comment. Default: "#".
var comment string

// When searching directories for test case files, only files whose names
// end with extension are considered. Default: ".test".
var extension string

// failCount counts the number of failed tests.
var failCount = 0

// errorCount counts the number of errors that are not considered test failures.
var errorCount = 0

// limit is the time within which a single test must complete
var limit time.Duration

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

	var help bool
	flag.StringVar(&comment, "c", "#", "comment delimiter for expected input and output")
	flag.StringVar(&extension, "e", ".test", "test case files have this extension")
	flag.BoolVar(&help, "h", false, "print this help information")
	flag.DurationVar(&limit, "t", 2*time.Second, "time limit for individual test cases")
	flag.BoolVar(&verbose, "v", false, "show verbose output")
	flag.CommandLine.Usage = usage
	flag.Parse()

	if help {
		flag.CommandLine.SetOutput(os.Stdout)
		usage()
		return
	}

	var program, roots []string
	for k, a := range flag.Args() {
		if a == "--" {
			// Allocate a spot for a test name in the program's command line
			program = make([]string, k, k+1)
			copy(program, flag.Args()[:k])
			roots = flag.Args()[k+1:]
		}
	}
	if len(program) == 0 {
		usage()
		log.Fatal("No program specified")
	} else if len(roots) == 0 {
		usage()
		log.Fatal("No test cases specified")
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
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()

	cmd := exec.CommandContext(ctx, program[0], append(program[1:], t.path)...)

	iPipe, oPipe, ePipe, e := attachPipes(cmd, t.path)
	if e != nil {
		// This is extremely unlikely. But if and when it happens, we won't start
		// the command. In this case, https://github.com/golang/go/issues/58369
		// suggests cancelling the context and then calling Start, which should
		// release the file descriptors without starting the command.
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

	lines := strings.SplitAfter(t.content, "\n")
	reads := 0
	readPrefix := comment + "<"
	for _, line := range lines {
		if strings.HasPrefix(line, readPrefix) {
			reads++
		}
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

	if ogot == "" {
		n, e := oPipe.Read(buf[:64])
		ogot = string(buf[:n])
		if e != nil && !errors.Is(e, io.EOF) {
			faile("error reading stdout", e)
			return
		}
	}
	if ogot != "" {
		log.Printf("%s: extra output: %s", t.path, ogot)
		fail()
		return
	}

	if egot == "" {
		n, e := ePipe.Read(buf[:64])
		egot = string(buf[:n])
		if e != nil && !errors.Is(e, io.EOF) {
			faile("error reading stderr", e)
			return
		}
	}
	if egot != "" {
		log.Printf("%s: extra error output: %s", t.path, egot)
		fail()
		return
	}

	if e := oPipe.Close(); e != nil {
		faile("closing test output", e)
		return
	}
	if e := ePipe.Close(); e != nil {
		faile("closing test error output", e)
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

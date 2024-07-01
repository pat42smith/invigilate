// Copyright 2024 Patrick Smith
// Use of this source code is subject to the MIT-style license in the LICENSE file.

package main

import (
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

// Function usage prints a usage message to stderr.
func usage() {
	fmt.Fprint(os.Stderr, `
Usage: invigilate [options] program -- files

Program invigilate runs a number of test cases against a single program.

The arguments between the last option and the "--" describe the program to be tested.

The arguments after "--" list files containing test cases. If one of these arguments
refers to a directory, the directory will be searched (recursively) for regular files
with the extension given by the -e option; these will be used as test cases.
Test case files listed directly in the command line do not need to end with
the extension given with -e.

The program being tested is run once for each test case. The command line consists
of the "program" part of the invigilate arguments, followed by one additional
argument, the path to the file containing the test case.

The expected results of a test case are described in comments embedded in the test file.
A line beginning with "#>" means that the remainder of the line should appear on standard
output; "#!", that the remainder should appear on the standard error output; and "#<",
that the remainder should be supplied to standard input. All of these are expected to
be produced or consumed in the order in which they appear in the test file. The -c option
may be used to specify another comment delimiter instead of "#", but the delimiter
must always appear at the beginning of a line.

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
	flag.DurationVar(&limit, "t", 2 * time.Second, "time limit for individual test cases")
	flag.BoolVar(&verbose, "v", false, "show verbose output")
	flag.CommandLine.Usage = usage
	flag.Parse()

	if help {
		usage()
		return
	}

	var program, roots []string
	for k, a := range flag.Args() {
		if a == "--" {
			// Allocate a spot for a test name in the program's command line
			program = make([]string, k, k + 1)
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
func findTests(roots []string, ch chan <-Test) {
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
func reportTest(path string, ch chan <-Test) {
	content, e := os.ReadFile(path)
	if e != nil {
		ch <- Test{path, "", e}
		return
	}
	ch <- Test{path, string(content), nil}
}

// Type Deadliner has os.File.SetDeadline
type Deadliner interface {
	SetDeadline(time.Time) error
}

// runTest runs a single test case
func runTest(t Test, program []string) {
	cmd := exec.Command(program[0], append(program[1:], t.path)...)
	deadline := time.Now().Add(limit)

	var iPipe io.WriteCloser
	var oPipe, ePipe io.ReadCloser
	pipeError := func(msg string, err error) {
		log.Printf("error %s for %s: %s", msg, t.path, err)
		errorCount++
		if iPipe != nil {
			iPipe.Close()
			cmd.Stdin.(io.Closer).Close()
		}
		if oPipe != nil {
			oPipe.Close()
			cmd.Stdout.(io.Closer).Close()
		}
		if ePipe != nil {
			ePipe.Close()
			cmd.Stderr.(io.Closer).Close()
		}
	}

	var e error
	if iPipe, e = cmd.StdinPipe(); e != nil {
		pipeError("opening input pipe", e)
		return
	}
	if e = iPipe.(Deadliner).SetDeadline(deadline); e != nil {
		pipeError("setting input deadline", e)
		return
	}

	if oPipe, e = cmd.StdoutPipe(); e != nil {
		pipeError("opening output pipe", e)
		return
	}
	if e = oPipe.(Deadliner).SetDeadline(deadline); e != nil {
		pipeError("setting output deadline", e)
		return
	}

	if ePipe, e = cmd.StderrPipe(); e != nil {
		pipeError("opening error output pipe", e)
		return
	}
	if e = ePipe.(Deadliner).SetDeadline(deadline); e != nil {
		pipeError("setting error output deadline", e)
		return
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
		iPipe.Close()
		oPipe.Close()
		ePipe.Close()
		go func(cmd *exec.Cmd) {
			time.Sleep(50 * time.Millisecond)
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			cmd.Wait()
		}(cmd)
		cmd = nil
	}

	faile := func(msg string, e error) {
		if errors.Is(e, os.ErrDeadlineExceeded) {
			log.Printf("%s: time limit exceeded", t.path)
		} else if e != nil {
			log.Printf("%s: %s: %s", t.path, msg, e)
		}
		fail()
	}

	buf := make([]byte, 65536)
	expect := func(pipe io.ReadCloser, what, want string, got *string) bool {
		for same, done := 0, false;; {
			for same < len(want) && same < len(*got) {
				if want[same] == (*got)[same] {
					same++
				} else {
					have := *got
					if n := strings.IndexByte(have, '\n'); n >= 0 {
						have = have[:n+1]
					}
					log.Printf("%s: incorrect %s", t.path, what)
					log.Printf("expected: %s", want)
					log.Printf("  actual: %s", have)
					fail()
					return false
				}
			}
			if same >= len(want) {
				*got = (*got)[len(want):]
				return true
			}
			if done {
				log.Printf("%s: incomplete %s", t.path, what)
				log.Printf("expected: %s", want)
				log.Printf("  actual: %s", *got)
				fail()
				return false
			}
			n, e := pipe.Read(buf)
			*got += string(buf[:n])
			if e == io.EOF {
				done = true
			} else if e != nil {
				faile("reading " + what, e)
				return false
			}
		}
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
			if e := iPipe.Close(); e != nil {
				faile("closing test input", e)
				return
			}
			reads = -1
		}
		if !strings.HasPrefix(line, comment) || len(line) < len(comment) + 2 {
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
			if !expect(oPipe, "test output", data, &ogot) {
				return
			}
		case '!':
			erred = true
			if !expect(ePipe, "test error output", data, &egot) {
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
			faile("output error", e)
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
			faile("output problem", e)
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
		if ee, ok := e.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			log.Printf("%s: %s", t.path, e)
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

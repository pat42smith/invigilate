# Go tests

[![Go Reference](https://pkg.go.dev/badge/github.com/pat42smith/invigilate.svg)](https://pkg.go.dev/github.com/pat42smith/invigilate)

Invigilate is a tool for running test cases. It allows testing a single program by running
several test cases against it. Each test case is contained in a single file, which contains
instructions describing the expected output from the test case.

Use standard Go tools to build invigilate, such as 'go build' or 'go install'.

There is a test suite, which may be run with 'go test'. It assumes that some standard
Unix tools, such as `sh`, `cp`, `awk`, and `sleep` can be found in `$PATH`.
Also, it has only been tested on Linux. So it may not work in other operating systems.
As well, some of the tests are sensitive to timing, so may fail sporadically
when the system is heavily loaded.

# Invigilate - a simple test case runner

Invigilate is a tool for running test cases. It allows testing a single program by running
several test cases against the program.
Each test case is contained in a single file, which contains
instructions describing the expected output from the test case. For example,
here is a small test case that expects to be run with /bin/sh:

```
echo "Hello, world!"
#>Hello, world!
```

The comment line beginning with `#>` specifies what should be printed when the test is run.

It is also possible, when invigilate is invoked with the `-a` option,
to add command line options for individual test cases. For example,

```
#- -x
echo Hello
#!+ echo Hello
#>Hello
```

Here, `#- -x` adds -x to the sh command line, and the `#!` line shows
the extra output expected on standard error.

For more information, run `invigilate -h`.

Use standard Go tools to build invigilate, such as 'go build' or 'go install'.

There is a test suite, which may be run with 'go test'. It assumes that some standard
Unix tools, such as `sh`, `cp`, `awk`, and `sleep` can be found in `$PATH`.
Also, it has only been tested on Linux. So it may not work in other operating systems.
As well, some of the tests are sensitive to timing, so may fail sporadically
when the system is heavily loaded.

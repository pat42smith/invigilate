# Copyright 2024 Patrick Smith
# Use of this source code is subject to the MIT-style license in the LICENSE file.
#
# Test the results from running invigilate in verbose mode when some tests fail.
# By running this as an invigilate test case, we can check not just that the standard output
# and error output are correct, but also that they are correctly interleaved.
#
# We assume that $INVIGILATE has been set to the location of invigilate.
#
# Note that when this is run, the current directory will be the main directory
# of the invigilate package.

$INVIGILATE -v /bin/sh -- testdata/mix

#>
#>testdata/mix/anteater.test
#>>anteater
#>
#>testdata/mix/bumblebee.test
#>>bumblebee
#!testdata/mix/bumblebee.test: incorrect test output
#!expected: bumblebee
#!  actual: hornet
#>
#>testdata/mix/corgi.test
#>>corgi
#>
#>testdata/mix/dingo.test
#>>dingo
#!testdata/mix/dingo.test: incorrect test output
#!expected: dingo
#!  actual: fox
#>
#>testdata/mix/elk.test
#>>elk
#!testdata/mix/elk.test: incorrect test output
#!expected: elk
#!  actual: moose
#>
#>testdata/mix/ferret.test
#>>ferret
#!3 failed tests

exit 1

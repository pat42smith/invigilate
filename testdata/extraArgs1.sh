# Copyright 2026 Patrick Smith
# Use of this source code is subject to the MIT-style license in the LICENSE file.
#
# Test using #- to add extra arguments.
#- -x

echo Hello
#!+ echo Hello
#>Hello

# And since we're writing to stderr, we want a non-0 exit code.
false
#!+ false

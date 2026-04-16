# Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
#
# Every architecture that you support should have a "phony" target, even
# if each architecture is built the same way. In the example here,
# architecture-specific binaries for the same C code are produced. If,
# for example, your module is written in pure Python, you should still
# have individual targets but they may all use the same "pip install"
# or similar recipe.
#
# You should edit this Makefile to take any steps needed to build your
# module. Either the result produces an executable artifact (script, binary,
# etc.) named "tqd-module" in this directory, or you will need to update
# the Dockerfile to include your artifacts, and update wrapper.sh to call
# your module logic appropriately.

.PHONY: amd64 arm64

amd64:
	gcc -march=x86-64 main.c -o tqd-module

arm64:
	gcc -march=armv8-a main.c -o tqd-module

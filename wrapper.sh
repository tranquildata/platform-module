#!/bin/bash
# Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
#
# This wrapper script is called by the API server to start the module logic
# itself. The default behavior provided here is simply to invoke the module
# entry-point, piping through standard IO, and terminating when the
# module itself terminates.
#
# You may edit this script to include any additional behavior that your
# module requires, typically inolving command-line flags, environment
# variables, or stream processing. By default, the Dockerfile uses Alpine
# Linux and installs bash to run this script. If you need additional tools
# you will need to update the Dockerfile to include them.
#
# The name of the module directive is available as $MODULE_DIRECTIVE.

/usr/local/bin/tqd-module
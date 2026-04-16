# Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
#
# This Dockerfile is used to build a complete, self-contained module image that
# can be deployed into the Tranquil Data platform. Each container instance
# accepts input and returns output via the API front-end, abstracting how external
# components work with the module logic. By the time this API is invoked, all
# authentication, policy, audit, and related tasks are known to have been
# handled, so modules can focus simply on their core logic.
#
# For modules that use compiled languages like C, you should stage your source
# code in the "module" directory and build from the Makefile there (like provided
# example already does). This will ensure that your binaries link correctly against
# the version libc and other components provided by this cersion of Alpine Linux.
#
# You should only modify this file to include additional packages to build
# and/or run your module logic or based on the output of the Makefile. See the
# steps below where it says "UPDATE HERE".

###
# step 1 of 3: build the golang-based generic API server
FROM --platform=$BUILDPLATFORM golang:1.25 AS apibuilder
ARG TARGETOS TARGETARCH

# copy everything under "api" into the builder image, and build the golang-based
# API server, which will be the entrpoint process for the image
RUN mkdir -p /tranquil
WORKDIR /tranquil
COPY api .
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -v -ldflags="-s" -o tqd-api .

###
# step 2 of 3: build and/or stage the module-spercific logic
FROM --platform=$BUILDPLATFORM alpine:3.23 AS modulebuilder
ARG TARGETARCH

# include "make" and "gcc" in the image used to build
# UPDATE HERE if you need additional tools to BUILD your module 
RUN apk add --no-cache build-base

# copy everything under "module" into the builder image and invoke the Makefile
WORKDIR /tranquil
COPY module .
RUN make $TARGETARCH

###
# step 3 of 3: build the runtime container image
FROM alpine:3.23

# include "bash" in the actual runtime image for the wrapper script
# UPDATE HERE if you need additional tools to RUN your module
RUN apk add --no-cache bash

# stage the built API as multi-arch artifacts, along with the wrapper script
# that will invoke the module logic
WORKDIR /usr/local/bin
COPY --from=apibuilder /tranquil/tqd-api .
COPY wrapper.sh ./tqd-module-wrapper
RUN chmod +x ./tqd-module-wrapper

# stage the IO direcotry with the directives file that defines how to interact
# with the module components, and setup permissions for any modules that use
# file-based input/output at runtime
RUN mkdir -p /moduleio
RUN mkdir -p /moduleio/output
RUN chmod 775 /moduleio
RUN mkdir -p /moduleio/output
COPY directives.json /moduleio

# UPDATE HERE if your makefile doesn't generate a runnable component named
# "tqd-module" and/or you need to include additional state in the image
COPY --from=modulebuilder /tranquil/tqd-module .

# finally, set the ENDPOINT to the API service to drive container interaction
ENTRYPOINT [ "/usr/local/bin/tqd-api" ]
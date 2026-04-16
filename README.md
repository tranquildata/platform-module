# Building platform modules for Tranquil Data #

This repository gives you all the tools to build a module that you can drop into your Tranquil Data-based platform. It is kept current by the Tranquil Data development team, and is used in internal platform testing, so building your modules images from this repository is the easiest way to ensure compliant intergration. After you clone the contents of this repo, you will need to do three things:

1. Place all of your module logic (source code, data files, etc.) in the `module` directory, edit the `Makefile` to build & stage your module, and edit `directives.json` for all directives that your module supports (below)
2. (optional) Edit `Dockerfile` to include depdencies and/or stage your module in the runtime image
3. (optional) Edit `wrapper.sh` to support anything your module needs when it is invoked
4. Build the image by running: `docker build --platform [PLATFORMS] -t tranquildata/platform-module .`

For step 3 you need to specify at least one platform. For instance to build an image that will run in AWS, you will specify "linux/amd64". If you're devloping and testing on a Mac laptop, you would use "linux/arm64". If you want to automate image building in once place, you can specify both of these platforms to get a multi-architecture image.

For step 2 there should be a target in the Makefile for each architecture that you target in step 3. Note that even if your module is chipset-agnositc (like a python script) you still need to provide platform list because the API server is written in Golang and will need a separate binary for each targetted architecture.

Note that you should you not change any of the API code to stage your module. If you find issues, or would like to suggest enhancements, please raise it in the github project! 

## Module Definitions ##

In Step 1 above, you edit the `directives.json` file to define the behavior of your module. This is a `json` file of the form:

```json
[
    {
        "name": "DIRECTIVE NAME",
        "batch": {
            "files": false,
        }
    }
]
```

Each entry in this list defines a way that the module can be invoked. The `DIRECTIVE NAME` is a unique way to invoke your module. If this directive runs interactively, then you omit the `batch` element. If the directive runs in a batch-mode (accepts input, returns output and then shuts down) you include `batch` and specify the IO type you want with the `files` attribute.

Your module can do IO in one of two ways. In batch-mode, it can accept a single file as input and write any number of files as output. This is what happens when `files` is set to `true`. The input file will be `/moduleio/input` and any output files should be written to `/mooduleio/output`. When an API call is made to the container, the input file is written with the API input, and the your module is started. The API service waits for your module to terminate, and returns all files from the output directory.

In interactive-mode, or in batch-mode when `files` is set to `false`, standard input and output is used. For each API call with input data your module can read the input via standard-in. Similarly, any output from your module shuold be written to standard-out. In batch-mode, an input API call will read all output from the module until EOF is reached (meaning that your module has terminated) before returning data to the caller.

Here's an example:

```json
[
    {
        "name": "montecarlo",
        "batch": {
            "files": true
        }
    }
]
```

This definition says that your module can be invoked with the directive `montecarlo` and will run in batch-mode expecting an input file and writing out a collection of output files before terminating. The environment variable `MODULE_DIRECTIVE` is available to the wrpper script and your module, so if you decide to define multiple directives within a module you know what you are being called to run.

## Container Runtime ##

The container that you build with this project will be started on-demand when customer API calls to the Data Platform ask to run this module. When a container instance is started, it is given a single directive as an environment variable. A simple API front-end server (see the `api` directory) is started, and it listens for input to provide the appropriate IO pattern expected by the module code. The API service keeps the module implementation both cloud and platform neutral, and isolated from policy, security, storage, audit, etc.

You can test that you've built your module container correctly by runnig it and providing input via API. For instance, if you build the vanilla contaier by checking out this repository and simply running step 4 above, you can then run:

```bash
docker run -e MODULE_DIRECTIVE=hello -p 8080:80 "tranquildata/platform-module" start
```

You now have a container instance running with the local port `8080` accessible. You can test this by running `curl -I "http://localhost:8080"` and you should see `HTTP/1.1 200 OK`.

To give the module input, run `curl -X PUT "http://localhost:8080" -d 'alice'`. The module will process the input "alice" and return the value "hello alice" that is base64-encoded before being returned to the API caller. Because this is a batch-mode module directive, as soon as the return value is provided the module terminates, then the API server shuts down and the container instance exists. 
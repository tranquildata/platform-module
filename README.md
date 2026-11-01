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

In Step 1 above, the `directives.json` file defines the behavior of a module. This is a `json` file of the form:

```json
[
    {
        "name": "DIRECTIVE NAME",
        "streaming": false,
        "parameters": [
            {
                "name": "PARAMETER NAME",
                "required": false,
                "default": 1.0
            }
        ]
    }
]
```

Each entry in this list defines a uniquely named configuration to run a module. The `DIRECTIVE NAME` must be unique within the file. If `streaming` is true, then this is an interactive configuration that supports sustained input and output. Otherwise, the configuration is batch-oriented: it takes an arbitrary number of inputs, returns the associated outputs, and then shuts down.

Your module can do IO in one of two ways:

1. Batch configurations accept one or more input files and write one or more output files. The inputs files will be mounted into the container at `/moduleio/input` and the output files should be written to `/moduleio/output`. All input files will be staged before module code is started. The Golang API service waits for your module to terminate, and then returns all files from the output directory. Anything your module writes to standard-out or standard-err goes to the container log.
2. Interactive configurations use standard input and output. For each API call with input data your module can read the input via standard-in. Similarly, any output from your module is written to standard-out. Neither direction blocks the other. Standard-in stays open until the caller closes it (see below), so a module that reads until end-of-input will wait for that signal.

A directive can optionally declare the numeric parameters it accepts in `parameters`. Each parameter has a `name`, which is required and must be unique within the directive. `required` is optional and defaults to `false`. `default` is optional, is a number, and is omitted when the parameter has no default. Callers can look up a directive's parameters with `GET /parameters` (see the API reference below).

Here's an example:

```json
[
    {
        "name": "montecarlo",
        "streaming": false,
        "parameters": [
            {
                "name": "iterations",
                "required": true
            },
            {
                "name": "tolerance",
                "default": 0.05
            }
        ]
    }
]
```

This definition says that your module can be invoked with the directive `montecarlo` and will run as a batch configuration, expecting input files, doing some work, and writing output files before terminating. It declares a required `iterations` parameter and an optional `tolerance` parameter that defaults to `0.05`. The environment variable `MODULE_DIRECTIVE` is available to the wrpper script and your module, so if you decide to define multiple directives within a module you know what you are being called to run.

The API service will refuse to start if `directives.json` contains duplicate or missing directive or parameter names, or any fields other than those described above.

## Container Runtime ##

When a container instance is started, it is given a single directive as an environment variable. A simple API front-end server (see the `api` directory) is started, and it listens for input to provide the appropriate IO pattern expected by the module code. The API service keeps the module implementation both cloud and platform neutral, and isolated from policy, security, storage, audit, etc.

You can test that you've built your module container correctly by running it and providing input via API. For instance, if you build the vanilla container by checking out this repository and simply running step 4 above, you can then run:

```bash
docker run -e MODULE_DIRECTIVE=hello -p 8080:80 "tranquildata/platform-module" start
```

You now have a container instance running with the local port `8080` accessible. You can test this by running `curl -I "http://localhost:8080"` and you should see `HTTP/1.1 200 OK`.

To give the module input, run `curl -X PUT "http://localhost:8080" -d 'alice'`. The module will process the input "alice" and return the base64-encoded value "hello alice". This example module directive will shut down the container instance as soon as it has responded to this message.

You can also start a container without setting `MODULE_DIRECTIVE`. The module is never started, and the container only describes its directives: `HEAD /`, `OPTIONS /` and `GET /parameters` are available, and every other call returns `400`. The container shuts down after 20 minutes. For example:

```bash
docker run -p 8080:80 "tranquildata/platform-module" start
curl -X OPTIONS "http://localhost:8080"
```

## API Reference ##

All endpoints are on the root path of the container's API port. These endpoints are available in every configuration:

* `HEAD /` returns `200 OK` once the API service is running.
* `OPTIONS /` returns the configuration as JSON, for example `{"directive": "hello", "streaming": true}`. If no directive is set, it returns a JSON array with an entry of the same form for every directive, in the order they appear in `directives.json`, for example `[{"directive": "hello", "streaming": true}]`.
* `GET /parameters?directive=NAME` returns the parameters declared for the directive `NAME` as a JSON array, for example `[{"name": "iterations", "required": true}, {"name": "tolerance", "required": false, "default": 0.05}]`. `NAME` can be any directive in `directives.json`, not only the one this container is running. A directive with no parameters returns `[]`. An unknown directive returns `404`.

Calls that return module output use this JSON form, where each value is base64-encoded:

```json
{
    "outputs": {
        "OUTPUT NAME": "BASE64 CONTENT"
    },
    "cursorOffset": 0
}
```

For batch configurations each key is the path of an output file, relative to `/moduleio/output`. Files at the top level and one directory down are returned. For streaming configurations there is a single entry whose key is the empty string `""`.

### Batch Configurations ###

Input is a JSON object that maps each input file name to its base64-encoded content, for example `{"input01.csv": "Zmlyc3Q="}`.

* `PUT /` stages the input files, starts the module, waits for it to terminate, and returns all of the output files in a single response. The container then shuts down.

For long-running modules, or inputs too large for a single call, use the asynchronous endpoints. Each takes a `runId` query parameter that you choose, and every call must use the same `runId`.

* `PUT /async?runId=ID&uploadOnly=true` stages input files without starting the module. You can call this as many times as you need.
* `PUT /async?runId=ID` stages any input files in the body and then starts the module. It returns immediately with the `runId`.
* `GET /async?runId=ID` collects the output. It returns `504` while the module is still running, so retry until it doesn't. Output is returned in pages of about 64 MB. A non-zero `cursorOffset` means more output remains, so call again for the next page. `204` means the module finished without writing any output files.

Input that arrives after the module has started is rejected with `409`. Once the module has terminated, the container shuts down after the final page of output, the `204`, or the module's error has been returned. If no output is requested for 20 minutes after the module terminates, the container shuts down anyway.

### Streaming Configurations ###

* `PUT /` writes the request body, followed by a newline, to the module's standard-in. The first call starts the module. It returns the output that is ready. If none is ready yet, it waits up to one second for some.
* `GET /` returns any output the module has produced since the last call, without sending input. It waits the same way as `PUT /`.
* `DELETE /` closes the module's standard-in so that the module sees end-of-input, and then returns output the same way as `GET /`. After this, `PUT /` returns `409`.

Because output is collected independently of input, a response contains whatever output is ready when it is sent. That may not be the response to that call's own input. Use `GET /` to collect output that arrives later.

When the module terminates and all of its output has been returned, the container shuts down. If output remains and none is requested for 20 minutes after the module terminates, the container shuts down anyway. If the module exits with an error and has no remaining output, the call returns `500`.

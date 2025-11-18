# go.viam.com/utils

<p align="center">
  <a href="https://pkg.go.dev/go.viam.com/utils"><img src="https://pkg.go.dev/badge/go.viam.com/utils" alt="PkgGoDev"></a>
</a>
</p>


This is a set of go utilities you can use via importing `go.viam.com/utils`. 

## Development

We use [mise-en-place][mise] to manage required tooling and as a task runner. You can install it on MacOS and most Linux distributions with the following command:

```bash
curl https://mise.run | sh
```

Note that the install script will print instructions for activating mise in your shell that you will need to follow to get a working build environment.

Mise is also available in Homebrew and several package repositories. See the [official documentation][install-mise] for a list of installation methods.

Once mise is set up you can run `mise tasks` to view the available targets. Some common ones are:

- `mise r build` - Build protos and then go code
- `mise r lint` - Run all linters

To run tests that use a backing database, start a local mongo instance in Docker with
```bash
docker run -d --name goutils-db-test -p 27017:27017 ghcr.io/viamrobotics/docker-mongo-rs:8.0
```
Then, run tests while having `TEST_MONGODB_URI` set like
```bash
TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test <TEST PACKAGE>
```

## Examples

This library includes examples that demonstrate grpc functionality for a variety of contexts - see links for more information:
* [echo](https://github.com/viamrobotics/goutils/blob/main/rpc/examples/echo/README.md)

As a convenience, you can run the `mise` tasks for these examples from the root of this repository via:
```
mise r example-{name} {recipe}
```

For example, try running a simple echo server with:
```
mise r example-echo run-server
```

## Windows Support

Windows 10 22H2 and up.

### Development Dependencies

* bash (from https://gitforwindows.org/ is good)

Support is not well tested yet.

### Known Issues

* rpc: ICE between local connections found via ICE mDNS appear to be flaky in the establishment phase.

## License check

See https://github.com/viamrobotics/rdk#license-check for instructions on how update our license policy.

## License 
Copyright 2021-2024 Viam Inc.

Apache 2.0 - See [LICENSE](https://github.com/viamrobotics/goutils/blob/main/LICENSE) file

[mise]: https://mise.jdx.dev/
[install-mise]: https://mise.jdx.dev/installing-mise.html

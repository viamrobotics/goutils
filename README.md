# go.viam.com/utils

<p align="center">
  <a href="https://pkg.go.dev/go.viam.com/utils"><img src="https://pkg.go.dev/badge/go.viam.com/utils" alt="PkgGoDev"></a>
</a>
</p>


This is a set of go utilities you can use via importing `go.viam.com/utils`. 

## Examples

This library includes examples that demonstrate grpc functionality for a variety of contexts - see links for more information:
* [echo](https://github.com/viamrobotics/goutils/blob/main/rpc/example/echo/README.md)
* [fileupload](https://github.com/viamrobotics/goutils/blob/main/rpc/example/fileupload/README.md)

As a convenience, you can run the `make` recipes for these examples from the root of this repository via:
```
make example-{name}/{recipe}
```

For example, try running a simple echo server with:
```
make example-echo/run-server
```

## Windows Support

### Development Dependencies

* bash (from https://gitforwindows.org/ is good)
* gcc (see https://www.msys2.org/)

Support is not well tested yet.

## License 
Copyright 2021-2022 Viam Inc.

Apache 2.0 - See [LICENSE](https://github.com/viamrobotics/goutils/blob/main/LICENSE) file

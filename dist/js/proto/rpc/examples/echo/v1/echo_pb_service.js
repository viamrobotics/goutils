// package: proto.rpc.examples.echo.v1
// file: proto/rpc/examples/echo/v1/echo.proto

var proto_rpc_examples_echo_v1_echo_pb = require("../../../../../proto/rpc/examples/echo/v1/echo_pb");
var grpc = require("@improbable-eng/grpc-web").grpc;

var EchoService = (function () {
  function EchoService() {}
  EchoService.serviceName = "proto.rpc.examples.echo.v1.EchoService";
  return EchoService;
}());

EchoService.Echo = {
  methodName: "Echo",
  service: EchoService,
  requestStream: false,
  responseStream: false,
  requestType: proto_rpc_examples_echo_v1_echo_pb.EchoRequest,
  responseType: proto_rpc_examples_echo_v1_echo_pb.EchoResponse
};

EchoService.EchoMultiple = {
  methodName: "EchoMultiple",
  service: EchoService,
  requestStream: false,
  responseStream: true,
  requestType: proto_rpc_examples_echo_v1_echo_pb.EchoMultipleRequest,
  responseType: proto_rpc_examples_echo_v1_echo_pb.EchoMultipleResponse
};

EchoService.EchoBiDi = {
  methodName: "EchoBiDi",
  service: EchoService,
  requestStream: true,
  responseStream: true,
  requestType: proto_rpc_examples_echo_v1_echo_pb.EchoBiDiRequest,
  responseType: proto_rpc_examples_echo_v1_echo_pb.EchoBiDiResponse
};

exports.EchoService = EchoService;

function EchoServiceClient(serviceHost, options) {
  this.serviceHost = serviceHost;
  this.options = options || {};
}

EchoServiceClient.prototype.echo = function echo(requestMessage, metadata, callback) {
  if (arguments.length === 2) {
    callback = arguments[1];
  }
  var client = grpc.unary(EchoService.Echo, {
    request: requestMessage,
    host: this.serviceHost,
    metadata: metadata,
    transport: this.options.transport,
    debug: this.options.debug,
    onEnd: function (response) {
      if (callback) {
        if (response.status !== grpc.Code.OK) {
          var err = new Error(response.statusMessage);
          err.code = response.status;
          err.metadata = response.trailers;
          callback(err, null);
        } else {
          callback(null, response.message);
        }
      }
    }
  });
  return {
    cancel: function () {
      callback = null;
      client.close();
    }
  };
};

EchoServiceClient.prototype.echoMultiple = function echoMultiple(requestMessage, metadata) {
  var listeners = {
    data: [],
    end: [],
    status: []
  };
  var client = grpc.invoke(EchoService.EchoMultiple, {
    request: requestMessage,
    host: this.serviceHost,
    metadata: metadata,
    transport: this.options.transport,
    debug: this.options.debug,
    onMessage: function (responseMessage) {
      listeners.data.forEach(function (handler) {
        handler(responseMessage);
      });
    },
    onEnd: function (status, statusMessage, trailers) {
      listeners.status.forEach(function (handler) {
        handler({ code: status, details: statusMessage, metadata: trailers });
      });
      listeners.end.forEach(function (handler) {
        handler({ code: status, details: statusMessage, metadata: trailers });
      });
      listeners = null;
    }
  });
  return {
    on: function (type, handler) {
      listeners[type].push(handler);
      return this;
    },
    cancel: function () {
      listeners = null;
      client.close();
    }
  };
};

EchoServiceClient.prototype.echoBiDi = function echoBiDi(metadata) {
  var listeners = {
    data: [],
    end: [],
    status: []
  };
  var client = grpc.client(EchoService.EchoBiDi, {
    host: this.serviceHost,
    metadata: metadata,
    transport: this.options.transport
  });
  client.onEnd(function (status, statusMessage, trailers) {
    listeners.status.forEach(function (handler) {
      handler({ code: status, details: statusMessage, metadata: trailers });
    });
    listeners.end.forEach(function (handler) {
      handler({ code: status, details: statusMessage, metadata: trailers });
    });
    listeners = null;
  });
  client.onMessage(function (message) {
    listeners.data.forEach(function (handler) {
      handler(message);
    })
  });
  client.start(metadata);
  return {
    on: function (type, handler) {
      listeners[type].push(handler);
      return this;
    },
    write: function (requestMessage) {
      client.send(requestMessage);
      return this;
    },
    end: function () {
      client.finishSend();
    },
    cancel: function () {
      listeners = null;
      client.close();
    }
  };
};

exports.EchoServiceClient = EchoServiceClient;


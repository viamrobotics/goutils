// package: proto.rpc.v1
// file: proto/rpc/v1/auth.proto

var proto_rpc_v1_auth_pb = require("../../../proto/rpc/v1/auth_pb");
var grpc = require("@improbable-eng/grpc-web").grpc;

var AuthService = (function () {
  function AuthService() {}
  AuthService.serviceName = "proto.rpc.v1.AuthService";
  return AuthService;
}());

AuthService.Authenticate = {
  methodName: "Authenticate",
  service: AuthService,
  requestStream: false,
  responseStream: false,
  requestType: proto_rpc_v1_auth_pb.AuthenticateRequest,
  responseType: proto_rpc_v1_auth_pb.AuthenticateResponse
};

exports.AuthService = AuthService;

function AuthServiceClient(serviceHost, options) {
  this.serviceHost = serviceHost;
  this.options = options || {};
}

AuthServiceClient.prototype.authenticate = function authenticate(requestMessage, metadata, callback) {
  if (arguments.length === 2) {
    callback = arguments[1];
  }
  var client = grpc.unary(AuthService.Authenticate, {
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

exports.AuthServiceClient = AuthServiceClient;


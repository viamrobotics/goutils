// package: proto.rpc.webrtc.v1
// file: proto/rpc/webrtc/v1/signaling.proto

var proto_rpc_webrtc_v1_signaling_pb = require("../../../../proto/rpc/webrtc/v1/signaling_pb");
var grpc = require("@improbable-eng/grpc-web").grpc;

var SignalingService = (function () {
  function SignalingService() {}
  SignalingService.serviceName = "proto.rpc.webrtc.v1.SignalingService";
  return SignalingService;
}());

SignalingService.Call = {
  methodName: "Call",
  service: SignalingService,
  requestStream: false,
  responseStream: true,
  requestType: proto_rpc_webrtc_v1_signaling_pb.CallRequest,
  responseType: proto_rpc_webrtc_v1_signaling_pb.CallResponse
};

SignalingService.CallUpdate = {
  methodName: "CallUpdate",
  service: SignalingService,
  requestStream: false,
  responseStream: false,
  requestType: proto_rpc_webrtc_v1_signaling_pb.CallUpdateRequest,
  responseType: proto_rpc_webrtc_v1_signaling_pb.CallUpdateResponse
};

SignalingService.Answer = {
  methodName: "Answer",
  service: SignalingService,
  requestStream: true,
  responseStream: true,
  requestType: proto_rpc_webrtc_v1_signaling_pb.AnswerResponse,
  responseType: proto_rpc_webrtc_v1_signaling_pb.AnswerRequest
};

SignalingService.OptionalWebRTCConfig = {
  methodName: "OptionalWebRTCConfig",
  service: SignalingService,
  requestStream: false,
  responseStream: false,
  requestType: proto_rpc_webrtc_v1_signaling_pb.OptionalWebRTCConfigRequest,
  responseType: proto_rpc_webrtc_v1_signaling_pb.OptionalWebRTCConfigResponse
};

exports.SignalingService = SignalingService;

function SignalingServiceClient(serviceHost, options) {
  this.serviceHost = serviceHost;
  this.options = options || {};
}

SignalingServiceClient.prototype.call = function call(requestMessage, metadata) {
  var listeners = {
    data: [],
    end: [],
    status: []
  };
  var client = grpc.invoke(SignalingService.Call, {
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

SignalingServiceClient.prototype.callUpdate = function callUpdate(requestMessage, metadata, callback) {
  if (arguments.length === 2) {
    callback = arguments[1];
  }
  var client = grpc.unary(SignalingService.CallUpdate, {
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

SignalingServiceClient.prototype.answer = function answer(metadata) {
  var listeners = {
    data: [],
    end: [],
    status: []
  };
  var client = grpc.client(SignalingService.Answer, {
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

SignalingServiceClient.prototype.optionalWebRTCConfig = function optionalWebRTCConfig(requestMessage, metadata, callback) {
  if (arguments.length === 2) {
    callback = arguments[1];
  }
  var client = grpc.unary(SignalingService.OptionalWebRTCConfig, {
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

exports.SignalingServiceClient = SignalingServiceClient;


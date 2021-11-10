// package: proto.rpc.examples.fileupload.v1
// file: proto/rpc/examples/fileupload/v1/fileupload.proto

var proto_rpc_examples_fileupload_v1_fileupload_pb = require("../../../../../proto/rpc/examples/fileupload/v1/fileupload_pb");
var grpc = require("@improbable-eng/grpc-web").grpc;

var FileUploadService = (function () {
  function FileUploadService() {}
  FileUploadService.serviceName = "proto.rpc.examples.fileupload.v1.FileUploadService";
  return FileUploadService;
}());

FileUploadService.UploadFile = {
  methodName: "UploadFile",
  service: FileUploadService,
  requestStream: true,
  responseStream: true,
  requestType: proto_rpc_examples_fileupload_v1_fileupload_pb.UploadFileRequest,
  responseType: proto_rpc_examples_fileupload_v1_fileupload_pb.UploadFileResponse
};

exports.FileUploadService = FileUploadService;

function FileUploadServiceClient(serviceHost, options) {
  this.serviceHost = serviceHost;
  this.options = options || {};
}

FileUploadServiceClient.prototype.uploadFile = function uploadFile(metadata) {
  var listeners = {
    data: [],
    end: [],
    status: []
  };
  var client = grpc.client(FileUploadService.UploadFile, {
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

exports.FileUploadServiceClient = FileUploadServiceClient;


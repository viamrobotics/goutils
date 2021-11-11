/**
 * @fileoverview gRPC-Web generated client stub for proto.rpc.webrtc.v1
 * @enhanceable
 * @public
 */

// GENERATED CODE -- DO NOT EDIT!


/* eslint-disable */
// @ts-nocheck



const grpc = {};
grpc.web = require('grpc-web');


var google_api_annotations_pb = require('../../../../google/api/annotations_pb.js')

var google_rpc_status_pb = require('../../../../google/rpc/status_pb.js')
const proto = {};
proto.proto = {};
proto.proto.rpc = {};
proto.proto.rpc.webrtc = {};
proto.proto.rpc.webrtc.v1 = require('./signaling_pb.js');

/**
 * @param {string} hostname
 * @param {?Object} credentials
 * @param {?grpc.web.ClientOptions} options
 * @constructor
 * @struct
 * @final
 */
proto.proto.rpc.webrtc.v1.SignalingServiceClient =
    function(hostname, credentials, options) {
  if (!options) options = {};
  options.format = 'text';

  /**
   * @private @const {!grpc.web.GrpcWebClientBase} The client
   */
  this.client_ = new grpc.web.GrpcWebClientBase(options);

  /**
   * @private @const {string} The hostname
   */
  this.hostname_ = hostname;

};


/**
 * @param {string} hostname
 * @param {?Object} credentials
 * @param {?grpc.web.ClientOptions} options
 * @constructor
 * @struct
 * @final
 */
proto.proto.rpc.webrtc.v1.SignalingServicePromiseClient =
    function(hostname, credentials, options) {
  if (!options) options = {};
  options.format = 'text';

  /**
   * @private @const {!grpc.web.GrpcWebClientBase} The client
   */
  this.client_ = new grpc.web.GrpcWebClientBase(options);

  /**
   * @private @const {string} The hostname
   */
  this.hostname_ = hostname;

};


/**
 * @const
 * @type {!grpc.web.MethodDescriptor<
 *   !proto.proto.rpc.webrtc.v1.CallRequest,
 *   !proto.proto.rpc.webrtc.v1.CallResponse>}
 */
const methodDescriptor_SignalingService_Call = new grpc.web.MethodDescriptor(
  '/proto.rpc.webrtc.v1.SignalingService/Call',
  grpc.web.MethodType.UNARY,
  proto.proto.rpc.webrtc.v1.CallRequest,
  proto.proto.rpc.webrtc.v1.CallResponse,
  /**
   * @param {!proto.proto.rpc.webrtc.v1.CallRequest} request
   * @return {!Uint8Array}
   */
  function(request) {
    return request.serializeBinary();
  },
  proto.proto.rpc.webrtc.v1.CallResponse.deserializeBinary
);


/**
 * @param {!proto.proto.rpc.webrtc.v1.CallRequest} request The
 *     request proto
 * @param {?Object<string, string>} metadata User defined
 *     call metadata
 * @param {function(?grpc.web.RpcError, ?proto.proto.rpc.webrtc.v1.CallResponse)}
 *     callback The callback function(error, response)
 * @return {!grpc.web.ClientReadableStream<!proto.proto.rpc.webrtc.v1.CallResponse>|undefined}
 *     The XHR Node Readable Stream
 */
proto.proto.rpc.webrtc.v1.SignalingServiceClient.prototype.call =
    function(request, metadata, callback) {
  return this.client_.rpcCall(this.hostname_ +
      '/proto.rpc.webrtc.v1.SignalingService/Call',
      request,
      metadata || {},
      methodDescriptor_SignalingService_Call,
      callback);
};


/**
 * @param {!proto.proto.rpc.webrtc.v1.CallRequest} request The
 *     request proto
 * @param {?Object<string, string>=} metadata User defined
 *     call metadata
 * @return {!Promise<!proto.proto.rpc.webrtc.v1.CallResponse>}
 *     Promise that resolves to the response
 */
proto.proto.rpc.webrtc.v1.SignalingServicePromiseClient.prototype.call =
    function(request, metadata) {
  return this.client_.unaryCall(this.hostname_ +
      '/proto.rpc.webrtc.v1.SignalingService/Call',
      request,
      metadata || {},
      methodDescriptor_SignalingService_Call);
};


/**
 * @const
 * @type {!grpc.web.MethodDescriptor<
 *   !proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigRequest,
 *   !proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigResponse>}
 */
const methodDescriptor_SignalingService_OptionalWebRTCConfig = new grpc.web.MethodDescriptor(
  '/proto.rpc.webrtc.v1.SignalingService/OptionalWebRTCConfig',
  grpc.web.MethodType.UNARY,
  proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigRequest,
  proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigResponse,
  /**
   * @param {!proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigRequest} request
   * @return {!Uint8Array}
   */
  function(request) {
    return request.serializeBinary();
  },
  proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigResponse.deserializeBinary
);


/**
 * @param {!proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigRequest} request The
 *     request proto
 * @param {?Object<string, string>} metadata User defined
 *     call metadata
 * @param {function(?grpc.web.RpcError, ?proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigResponse)}
 *     callback The callback function(error, response)
 * @return {!grpc.web.ClientReadableStream<!proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigResponse>|undefined}
 *     The XHR Node Readable Stream
 */
proto.proto.rpc.webrtc.v1.SignalingServiceClient.prototype.optionalWebRTCConfig =
    function(request, metadata, callback) {
  return this.client_.rpcCall(this.hostname_ +
      '/proto.rpc.webrtc.v1.SignalingService/OptionalWebRTCConfig',
      request,
      metadata || {},
      methodDescriptor_SignalingService_OptionalWebRTCConfig,
      callback);
};


/**
 * @param {!proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigRequest} request The
 *     request proto
 * @param {?Object<string, string>=} metadata User defined
 *     call metadata
 * @return {!Promise<!proto.proto.rpc.webrtc.v1.OptionalWebRTCConfigResponse>}
 *     Promise that resolves to the response
 */
proto.proto.rpc.webrtc.v1.SignalingServicePromiseClient.prototype.optionalWebRTCConfig =
    function(request, metadata) {
  return this.client_.unaryCall(this.hostname_ +
      '/proto.rpc.webrtc.v1.SignalingService/OptionalWebRTCConfig',
      request,
      metadata || {},
      methodDescriptor_SignalingService_OptionalWebRTCConfig);
};


module.exports = proto.proto.rpc.webrtc.v1;


/**
 * @fileoverview gRPC-Web generated client stub for proto.rpc.v1
 * @enhanceable
 * @public
 */

// GENERATED CODE -- DO NOT EDIT!


/* eslint-disable */
// @ts-nocheck



const grpc = {};
grpc.web = require('grpc-web');


var google_api_annotations_pb = require('../../../google/api/annotations_pb.js')
const proto = {};
proto.proto = {};
proto.proto.rpc = {};
proto.proto.rpc.v1 = require('./auth_pb.js');

/**
 * @param {string} hostname
 * @param {?Object} credentials
 * @param {?grpc.web.ClientOptions} options
 * @constructor
 * @struct
 * @final
 */
proto.proto.rpc.v1.AuthServiceClient =
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
proto.proto.rpc.v1.AuthServicePromiseClient =
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
 *   !proto.proto.rpc.v1.AuthenticateRequest,
 *   !proto.proto.rpc.v1.AuthenticateResponse>}
 */
const methodDescriptor_AuthService_Authenticate = new grpc.web.MethodDescriptor(
  '/proto.rpc.v1.AuthService/Authenticate',
  grpc.web.MethodType.UNARY,
  proto.proto.rpc.v1.AuthenticateRequest,
  proto.proto.rpc.v1.AuthenticateResponse,
  /**
   * @param {!proto.proto.rpc.v1.AuthenticateRequest} request
   * @return {!Uint8Array}
   */
  function(request) {
    return request.serializeBinary();
  },
  proto.proto.rpc.v1.AuthenticateResponse.deserializeBinary
);


/**
 * @param {!proto.proto.rpc.v1.AuthenticateRequest} request The
 *     request proto
 * @param {?Object<string, string>} metadata User defined
 *     call metadata
 * @param {function(?grpc.web.RpcError, ?proto.proto.rpc.v1.AuthenticateResponse)}
 *     callback The callback function(error, response)
 * @return {!grpc.web.ClientReadableStream<!proto.proto.rpc.v1.AuthenticateResponse>|undefined}
 *     The XHR Node Readable Stream
 */
proto.proto.rpc.v1.AuthServiceClient.prototype.authenticate =
    function(request, metadata, callback) {
  return this.client_.rpcCall(this.hostname_ +
      '/proto.rpc.v1.AuthService/Authenticate',
      request,
      metadata || {},
      methodDescriptor_AuthService_Authenticate,
      callback);
};


/**
 * @param {!proto.proto.rpc.v1.AuthenticateRequest} request The
 *     request proto
 * @param {?Object<string, string>=} metadata User defined
 *     call metadata
 * @return {!Promise<!proto.proto.rpc.v1.AuthenticateResponse>}
 *     Promise that resolves to the response
 */
proto.proto.rpc.v1.AuthServicePromiseClient.prototype.authenticate =
    function(request, metadata) {
  return this.client_.unaryCall(this.hostname_ +
      '/proto.rpc.v1.AuthService/Authenticate',
      request,
      metadata || {},
      methodDescriptor_AuthService_Authenticate);
};


module.exports = proto.proto.rpc.v1;


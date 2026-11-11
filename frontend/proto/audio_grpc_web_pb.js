/**
 * Cliente gRPC-Web para audio.AudioStreamer (compatible con grpc-web 1.5+).
 */
const grpc = {};
grpc.web = require("grpc-web");
const proto = { audio: require("./audio_pb.js") };

function AudioStreamerClient(hostname, credentials, options) {
  if (!options) options = {};
  options.format = options.format || "binary";

  this.client_ = new grpc.web.GrpcWebClientBase(options);
  this.hostname_ = hostname.replace(/\/+$/, "");
  this.credentials_ = credentials;
  this.options_ = options;
}

const methodDescriptor_AudioStreamer_StreamAudio =
  new grpc.web.MethodDescriptor(
    "/audio.AudioStreamer/StreamAudio",
    grpc.web.MethodType.SERVER_STREAMING,
    proto.audio.AudioRequest,
    proto.audio.AudioChunk,
    function (request) {
      return request.serializeBinary();
    },
    proto.audio.AudioChunk.deserializeBinary
  );

AudioStreamerClient.prototype.streamAudio = function (request, metadata) {
  return this.client_.serverStreaming(
    this.hostname_ + "/audio.AudioStreamer/StreamAudio",
    request,
    metadata || {},
    methodDescriptor_AudioStreamer_StreamAudio
  );
};

module.exports = {
  AudioStreamerClient,
};

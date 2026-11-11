/**
 * Stubs Protobuf para audio.proto (AudioRequest, AudioChunk) compatibles con
 * google-protobuf (protoc-gen-js / CommonJS).
 */
const jspb = require("google-protobuf");

const proto = { audio: {} };

/**
 * message AudioRequest { string titulo = 1; string formato = 2; }
 */
proto.audio.AudioRequest = function (opt_data) {
  jspb.Message.initialize(this, opt_data, 0, -1, null, null);
};
jspb.inherits(proto.audio.AudioRequest, jspb.Message);

proto.audio.AudioRequest.prototype.getTitulo = function () {
  return /** @type {string} */ (jspb.Message.getFieldWithDefault(this, 1, ""));
};

proto.audio.AudioRequest.prototype.setTitulo = function (value) {
  return jspb.Message.setProto3StringField(this, 1, value);
};

proto.audio.AudioRequest.prototype.getFormato = function () {
  return /** @type {string} */ (jspb.Message.getFieldWithDefault(this, 2, ""));
};

proto.audio.AudioRequest.prototype.setFormato = function (value) {
  return jspb.Message.setProto3StringField(this, 2, value);
};

proto.audio.AudioRequest.prototype.serializeBinary = function () {
  const writer = new jspb.BinaryWriter();
  proto.audio.AudioRequest.serializeBinaryToWriter(this, writer);
  return writer.getResultBuffer();
};

proto.audio.AudioRequest.serializeBinaryToWriter = function (message, writer) {
  const titulo = message.getTitulo();
  if (titulo.length > 0) {
    writer.writeString(1, titulo);
  }
  const formato = message.getFormato();
  if (formato.length > 0) {
    writer.writeString(2, formato);
  }
};

proto.audio.AudioRequest.deserializeBinary = function (bytes) {
  const reader = new jspb.BinaryReader(bytes);
  const msg = new proto.audio.AudioRequest();
  return proto.audio.AudioRequest.deserializeBinaryFromReader(msg, reader);
};

proto.audio.AudioRequest.deserializeBinaryFromReader = function (msg, reader) {
  while (reader.nextField()) {
    if (reader.isEndGroup()) break;
    switch (reader.getFieldNumber()) {
      case 1:
        msg.setTitulo(reader.readString());
        break;
      case 2:
        msg.setFormato(reader.readString());
        break;
      default:
        reader.skipField();
        break;
    }
  }
  return msg;
};

/**
 * message AudioChunk { bytes data = 1; }
 */
proto.audio.AudioChunk = function (opt_data) {
  jspb.Message.initialize(this, opt_data, 0, -1, null, null);
};
jspb.inherits(proto.audio.AudioChunk, jspb.Message);

proto.audio.AudioChunk.prototype.getData = function () {
  return /** @type {string|!Uint8Array} */ (
    jspb.Message.getFieldWithDefault(this, 1, "")
  );
};

proto.audio.AudioChunk.prototype.getData_asU8 = function () {
  return /** @type {!Uint8Array} */ (
    jspb.Message.bytesAsU8(this.getData())
  );
};

proto.audio.AudioChunk.prototype.setData = function (value) {
  return jspb.Message.setProto3BytesField(this, 1, value);
};

proto.audio.AudioChunk.prototype.serializeBinary = function () {
  const writer = new jspb.BinaryWriter();
  proto.audio.AudioChunk.serializeBinaryToWriter(this, writer);
  return writer.getResultBuffer();
};

proto.audio.AudioChunk.serializeBinaryToWriter = function (message, writer) {
  const data = message.getData_asU8();
  if (data.length > 0) {
    writer.writeBytes(1, data);
  }
};

proto.audio.AudioChunk.deserializeBinary = function (bytes) {
  const reader = new jspb.BinaryReader(bytes);
  const msg = new proto.audio.AudioChunk();
  return proto.audio.AudioChunk.deserializeBinaryFromReader(msg, reader);
};

proto.audio.AudioChunk.deserializeBinaryFromReader = function (msg, reader) {
  while (reader.nextField()) {
    if (reader.isEndGroup()) break;
    switch (reader.getFieldNumber()) {
      case 1:
        msg.setData(reader.readBytes());
        break;
      default:
        reader.skipField();
        break;
    }
  }
  return msg;
};

module.exports = proto.audio;

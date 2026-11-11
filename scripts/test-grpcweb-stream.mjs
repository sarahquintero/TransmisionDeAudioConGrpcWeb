#!/usr/bin/env node
/**
 * Prueba de integración gRPC-Web (sin dependencias externas).
 *
 * Envía un AudioRequest { titulo, formato } en formato gRPC-Web binario y
 * decodifica en tiempo real los frames AudioChunk (bloques de 64 KB) y los
 * trailers gRPC (grpc-status).
 *
 * Uso:
 *   node scripts/test-grpcweb-stream.mjs
 *   node scripts/test-grpcweb-stream.mjs --url http://localhost:8000/grpc/audio.AudioStreamer/StreamAudio
 */

const args = process.argv.slice(2);
const getArg = (flag, def) => {
  const idx = args.indexOf(flag);
  return idx !== -1 && args[idx + 1] ? args[idx + 1] : def;
};

const targetUrl = getArg("--url", "http://localhost:8080/audio.AudioStreamer/StreamAudio");
const titulo = getArg("--titulo", "sample");
const formato = getArg("--formato", "mp3");

// --- Codificación Protobuf mínima para AudioRequest -----------------------
function encodeVarint(n) {
  const out = [];
  while (n >= 0x80) {
    out.push((n & 0x7f) | 0x80);
    n >>>= 7;
  }
  out.push(n & 0x7f);
  return Buffer.from(out);
}

function encodeStringField(fieldNumber, value) {
  if (!value) return Buffer.alloc(0);
  const strBuf = Buffer.from(value, "utf8");
  const tag = Buffer.from([(fieldNumber << 3) | 2]); // wire type 2 = length-delimited
  return Buffer.concat([tag, encodeVarint(strBuf.length), strBuf]);
}

function encodeAudioRequest(tituloStr, formatoStr) {
  const proto = Buffer.concat([
    encodeStringField(1, tituloStr),
    encodeStringField(2, formatoStr),
  ]);
  // Frame gRPC-Web: 1 byte flags (0x00 = datos) + 4 bytes longitud big-endian
  const header = Buffer.alloc(5);
  header.writeUInt8(0x00, 0);
  header.writeUInt32BE(proto.length, 1);
  return Buffer.concat([header, proto]);
}

// --- Decodificación Protobuf mínima para AudioChunk -----------------------
function decodeVarint(buf, offset) {
  let value = 0;
  let shift = 0;
  let pos = offset;
  while (pos < buf.length) {
    const b = buf[pos++];
    value |= (b & 0x7f) << shift;
    if ((b & 0x80) === 0) return { value: value >>> 0, next: pos };
    shift += 7;
  }
  throw new Error("Varint truncado en AudioChunk");
}

function decodeAudioChunk(protoBuf) {
  let pos = 0;
  let data = Buffer.alloc(0);
  while (pos < protoBuf.length) {
    const { value: tag, next } = decodeVarint(protoBuf, pos);
    pos = next;
    const fieldNumber = tag >>> 3;
    const wireType = tag & 0x07;
    if (wireType === 2) {
      const lenRes = decodeVarint(protoBuf, pos);
      pos = lenRes.next;
      const slice = protoBuf.subarray(pos, pos + lenRes.value);
      pos += lenRes.value;
      if (fieldNumber === 1) data = slice;
    } else {
      throw new Error(`Wire type inesperado (${wireType}) en AudioChunk`);
    }
  }
  return data;
}

// --- Ejecución de la prueba -----------------------------------------------
async function main() {
  console.log(`Target : ${targetUrl}`);
  console.log(`Request: AudioRequest { titulo: "${titulo}", formato: "${formato}" }\n`);

  // 1. Verificar preflight CORS (como haría el navegador)
  const preflight = await fetch(targetUrl, {
    method: "OPTIONS",
    headers: {
      Origin: "http://localhost:3000",
      "Access-Control-Request-Method": "POST",
      "Access-Control-Request-Headers": "content-type,x-grpc-web,x-user-agent",
    },
  });
  console.log(
    `[CORS Preflight] HTTP ${preflight.status} | Allow-Origin: ${preflight.headers.get("access-control-allow-origin")}`
  );

  // 2. Enviar petición gRPC-Web server-streaming
  const body = encodeAudioRequest(titulo, formato);
  const start = Date.now();
  const res = await fetch(targetUrl, {
    method: "POST",
    headers: {
      Origin: "http://localhost:3000",
      "Content-Type": "application/grpc-web+proto",
      "X-Grpc-Web": "1",
      "X-User-Agent": "grpc-web-javascript/0.1",
    },
    body,
  });

  console.log(
    `[HTTP Response]  HTTP ${res.status} | Content-Type: ${res.headers.get("content-type")} | Allow-Origin: ${res.headers.get("access-control-allow-origin")}`
  );
  if (!res.ok || !res.body) {
    throw new Error(`Respuesta HTTP inesperada: ${res.status}`);
  }

  let buffer = Buffer.alloc(0);
  let chunkCount = 0;
  let totalAudioBytes = 0;
  let trailersText = "";

  const reader = res.body.getReader();
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer = Buffer.concat([buffer, Buffer.from(value)]);

    while (buffer.length >= 5) {
      const flag = buffer.readUInt8(0);
      const len = buffer.readUInt32BE(1);
      if (buffer.length < 5 + len) break;

      const payload = buffer.subarray(5, 5 + len);
      buffer = buffer.subarray(5 + len);

      if ((flag & 0x80) === 0) {
        // Frame de datos (AudioChunk)
        const audioData = decodeAudioChunk(payload);
        chunkCount++;
        totalAudioBytes += audioData.length;
        const elapsed = String(Date.now() - start).padStart(4, " ");
        console.log(
          `[t+${elapsed}ms] Chunk #${String(chunkCount).padStart(2, "0")}: ${audioData.length} bytes (${(audioData.length / 1024).toFixed(2)} KB)`
        );
      } else {
        // Frame de trailers gRPC
        trailersText = payload.toString("utf8").trim();
      }
    }
  }

  console.log("\n--- Resumen ---");
  console.log(`Chunks recibidos : ${chunkCount}`);
  console.log(`Total audio      : ${totalAudioBytes} bytes (${(totalAudioBytes / 1024).toFixed(2)} KB)`);
  console.log(`Trailers gRPC    : ${trailersText.replace(/\r?\n/g, " | ")}`);
  console.log(`Duración total   : ${Date.now() - start} ms`);

  if (!trailersText.includes("grpc-status:0") && !trailersText.includes("grpc-status: 0")) {
    process.exitCode = 1;
  }
}

main().catch((err) => {
  console.error("Error en la prueba gRPC-Web:", err);
  process.exit(1);
});

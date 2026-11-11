const { AudioRequest } = require("./proto/audio_pb.js");
const { AudioStreamerClient } = require("./proto/audio_grpc_web_pb.js");

const API_BASE = window.API_GATEWAY_URL || "http://localhost:8000";
const GRPC_BASE = `${API_BASE}/grpc`;
const WS_URL = `${API_BASE.replace(/^http/i, "ws")}/api/reacciones/ws`;

const grpcClient = new AudioStreamerClient(GRPC_BASE, null, {
  format: "binary",
});

// client_id único persistido en localStorage para aislar el historial por navegador/usuario
function getOrCreateClientId() {
  const storageKey = "audio_grpcweb_client_id";
  let existing = "";
  try {
    existing = localStorage.getItem(storageKey) || "";
  } catch {
    existing = "";
  }
  if (existing) return existing;

  const generated =
    typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID()
      : `user-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;

  try {
    localStorage.setItem(storageKey, generated);
  } catch {
    // Ignoramos si localStorage no está disponible
  }
  return generated;
}

const clientId = getOrCreateClientId();
const userLabel = `Usuario ${clientId.slice(0, 6)}`;

// Esquema de metadatos por tipo de audio (según PDF)
const METADATA_SCHEMA = {
  "Música": [
    { key: "artista_principal", label: "Artista Principal", placeholder: "Ej. Artista Demo" },
    { key: "album", label: "Álbum", placeholder: "Ej. Album de Prueba" },
    { key: "genero_musical", label: "Género Musical", placeholder: "Ej. Rock" },
  ],
  Podcast: [
    { key: "nombre_podcast", label: "Nombre del Podcast", placeholder: "Ej. Sistemas Distribuidos" },
    { key: "titulo_episodio", label: "Título del Episodio", placeholder: "Ej. Introducción a gRPC" },
    { key: "anfitrion", label: "Anfitrión", placeholder: "Ej. Profesor X" },
  ],
  Audiolibro: [
    { key: "titulo_libro", label: "Título del Libro", placeholder: "Ej. El Quijote" },
    { key: "autor", label: "Autor", placeholder: "Ej. Miguel de Cervantes" },
    { key: "narrador", label: "Narrador", placeholder: "Ej. Actor de Voz" },
  ],
  "Ruido Blanco": [
    { key: "tipo_sonido", label: "Tipo de Sonido", placeholder: "Ej. Ruido Marrón" },
    { key: "fuente_audio", label: "Fuente de Audio", placeholder: "Ej. Lluvia" },
    { key: "uso_sugerido", label: "Uso Sugerido", placeholder: "Ej. Concentración" },
  ],
};

const META_LABELS = {
  artista_principal: "Artista",
  album: "Álbum",
  genero_musical: "Género",
  nombre_podcast: "Podcast",
  titulo_episodio: "Episodio",
  anfitrion: "Anfitrión",
  titulo_libro: "Libro",
  autor: "Autor",
  narrador: "Narrador",
  tipo_sonido: "Tipo de sonido",
  fuente_audio: "Fuente",
  uso_sugerido: "Uso sugerido",
};

// Referencias DOM
const el = {
  tipoAudio: document.getElementById("tipoAudio"),
  selectAudio: document.getElementById("selectAudio"),
  btnReproducir: document.getElementById("btnReproducir"),
  btnPlay: document.getElementById("btnPlay"),
  btnPause: document.getElementById("btnPause"),
  audioPlayer: document.getElementById("audioPlayer"),
  playerState: document.getElementById("playerState"),
  metadataBox: document.getElementById("metadataBox"),
  metadataTitle: document.getElementById("metadataTitle"),
  metadataList: document.getElementById("metadataList"),
  reactionsBar: document.getElementById("reactionsBar"),
  reactionsRoom: document.getElementById("reactionsRoom"),
  chatReacciones: document.getElementById("chat-reacciones"),
  selectPlan: document.getElementById("selectPlan"),
  btnPagar: document.getElementById("btnPagar"),
  inputToken: document.getElementById("inputToken"),
  btnReutilizarToken: document.getElementById("btnReutilizarToken"),
  tokenStatus: document.getElementById("tokenStatus"),
  listaHistorial: document.getElementById("listaHistorial"),
  historyUserBadge: document.getElementById("historyUserBadge"),
  inputArchivo: document.getElementById("inputArchivo"),
  adminTipoAudio: document.getElementById("adminTipoAudio"),
  adminMetadataFields: document.getElementById("adminMetadataFields"),
  btnRegistrarAudio: document.getElementById("btnRegistrarAudio"),
  systemLogs: document.getElementById("systemLogs"),
};

let activeStream = null;
let currentObjectUrl = null;
let currentAudioId = "";
let ws = null;
let wsReconnectTimer = null;
let catalogByTitle = new Map();

// --- Logs de aplicación ---------------------------------------------------
function logSystem(message) {
  const now = new Date().toLocaleTimeString("es-ES", { hour12: false });
  el.systemLogs.textContent += `[${now}] ${message}\n`;
  el.systemLogs.scrollTop = el.systemLogs.scrollHeight;
}

// --- Animación y Chat de Reacciones ---------------------------------------
function spawnFloatingEmoji(anchorEl, iconClass, emojiFallback) {
  if (!anchorEl) return;
  const rect = anchorEl.getBoundingClientRect();
  const floater = document.createElement("span");
  floater.className = "floating-reaction";
  floater.style.left = `${rect.left + rect.width / 2}px`;
  floater.style.top = `${rect.top + window.scrollY - 6}px`;

  if (iconClass) {
    const i = document.createElement("i");
    i.className = iconClass;
    const computedColor = window.getComputedStyle(anchorEl).color;
    if (computedColor) floater.style.color = computedColor;
    floater.appendChild(i);
  } else {
    floater.textContent = emojiFallback || "✨";
  }

  floater.addEventListener("animationend", () => floater.remove());
  document.body.appendChild(floater);
}

function clearReactionChat() {
  el.chatReacciones.innerHTML = "";
  const empty = document.createElement("span");
  empty.className = "chat-empty";
  empty.textContent = currentAudioId
    ? `Aún no hay reacciones en "${currentAudioId}".`
    : "Selecciona un audio para ver o enviar reacciones.";
  el.chatReacciones.appendChild(empty);
}

function appendReactionChat(text) {
  const empty = el.chatReacciones.querySelector(".chat-empty");
  if (empty) empty.remove();

  const row = document.createElement("div");
  const now = new Date().toLocaleTimeString("es-ES", { hour12: false });
  row.textContent = `[${now}] ${text}`;
  el.chatReacciones.appendChild(row);
  el.chatReacciones.scrollTop = el.chatReacciones.scrollHeight;
}

function updateReactionsRoomUI() {
  const buttons = el.reactionsBar.querySelectorAll(".reaction-btn");
  for (const btn of buttons) {
    btn.disabled = !currentAudioId;
  }
  el.reactionsRoom.textContent = currentAudioId
    ? `Sala: ${currentAudioId}`
    : "Sala: sin audio";
}

// --- WebSocket de Reacciones ----------------------------------------------
function sendWsSubscribe(audioId) {
  if (!ws || ws.readyState !== WebSocket.OPEN || !audioId) return;
  ws.send(
    JSON.stringify({
      type: "subscribe",
      audio_id: audioId,
      sender: userLabel,
    })
  );
}

function sendWsPayload(payload) {
  if (!ws || ws.readyState !== WebSocket.OPEN) {
    logSystem(`[WebSocket] No conectado; evento "${payload.type}" no enviado.`);
    return false;
  }
  if (!payload.audio_id) return false;
  ws.send(JSON.stringify(payload));
  return true;
}

function connectWebSocket() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  const url = currentAudioId
    ? `${WS_URL}?audio_id=${encodeURIComponent(currentAudioId)}`
    : WS_URL;

  ws = new WebSocket(url);

  ws.addEventListener("open", () => {
    logSystem("Conectado al servidor de reacciones");
    if (currentAudioId) {
      sendWsSubscribe(currentAudioId);
    }
  });

  ws.addEventListener("message", (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.type === "subscribed") {
        logSystem(`Suscrito al servidor de reacciones para "${msg.audio_id}".`);
        return;
      }

      // Aislamiento estricto en cliente: ignorar mensajes que no correspondan al audio actual
      if (!currentAudioId || msg.audio_id !== currentAudioId) {
        return;
      }

      const sender = msg.sender || "Otro usuario";
      if (msg.type === "reaction") {
        const emoji = msg.emoji || "👍";
        appendReactionChat(`${sender} reaccionó con ${emoji}`);
        logSystem(`Reacción recibida en "${msg.audio_id}": ${sender} reaccionó con ${emoji}`);
        const matchingBtn = el.reactionsBar.querySelector(
          `.reaction-btn[data-emoji="${CSS.escape(emoji)}"]`
        );
        spawnFloatingEmoji(matchingBtn || el.reactionsBar, msg.icon, emoji);
        return;
      }

      if (msg.type === "pause") {
        appendReactionChat(`${sender} pausó el audio`);
        logSystem(`Reacción recibida: otro usuario pausó el audio "${msg.audio_id}"`);
        return;
      }

      if (msg.type === "play") {
        appendReactionChat(`${sender} reprodujo el audio`);
        logSystem(`Reacción recibida: otro usuario reprodujo el audio "${msg.audio_id}"`);
        return;
      }

      logSystem(`Evento recibido (${msg.type}): audio "${msg.audio_id}"`);
    } catch {
      logSystem(`[WebSocket] ${event.data}`);
    }
  });

  ws.addEventListener("close", () => {
    clearTimeout(wsReconnectTimer);
    wsReconnectTimer = setTimeout(connectWebSocket, 3000);
  });

  ws.addEventListener("error", () => {
    logSystem("Error en la conexión WebSocket con el servidor de reacciones.");
  });
}

// --- Metadatos en el Reproductor ------------------------------------------
function renderSelectedMetadata() {
  const titulo = el.selectAudio.value;
  const audio = catalogByTitle.get(titulo);

  if (!titulo || !audio) {
    el.metadataBox.hidden = true;
    el.metadataList.innerHTML = "";
    el.btnReproducir.disabled = true;
    return;
  }

  el.btnReproducir.disabled = false;
  el.metadataTitle.textContent = `Metadatos: ${audio.titulo}.${audio.formato} (${audio.tipo})`;
  el.metadataList.innerHTML = "";

  const meta = audio.metadatos || {};
  const entries = Object.entries(meta);
  if (entries.length === 0) {
    const li = document.createElement("li");
    li.textContent = "Sin metadatos adicionales registrados.";
    el.metadataList.appendChild(li);
  } else {
    for (const [k, v] of entries) {
      const li = document.createElement("li");
      const label = META_LABELS[k] || k.replace(/_/g, " ");
      const strong = document.createElement("strong");
      strong.textContent = `${label}: `;
      li.appendChild(strong);
      li.appendChild(document.createTextNode(v));
      el.metadataList.appendChild(li);
    }
  }

  el.metadataBox.hidden = false;
}

// --- Catálogo de Audios ---------------------------------------------------
async function cargarCatalogo(tipo, preferTitulo = "") {
  catalogByTitle.clear();
  el.selectAudio.innerHTML = "";

  if (!tipo) {
    const opt = document.createElement("option");
    opt.value = "";
    opt.textContent = "-- Seleccione un tipo primero --";
    el.selectAudio.appendChild(opt);
    currentAudioId = "";
    updateReactionsRoomUI();
    clearReactionChat();
    renderSelectedMetadata();
    return;
  }

  try {
    const res = await fetch(`${API_BASE}/api/audios/lista?tipo=${encodeURIComponent(tipo)}`);
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `HTTP ${res.status}`);
    }

    const audios = data.audios || [];
    if (audios.length === 0) {
      const opt = document.createElement("option");
      opt.value = "";
      opt.textContent = "-- No hay audios para este tipo --";
      el.selectAudio.appendChild(opt);
      currentAudioId = "";
      updateReactionsRoomUI();
      clearReactionChat();
      renderSelectedMetadata();
      return;
    }

    for (const audio of audios) {
      catalogByTitle.set(audio.titulo, audio);
      const opt = document.createElement("option");
      opt.value = audio.titulo;
      opt.dataset.formato = audio.formato || "mp3";
      opt.textContent = `${audio.titulo}.${audio.formato || "mp3"}`;
      el.selectAudio.appendChild(opt);
    }

    if (preferTitulo && catalogByTitle.has(preferTitulo)) {
      el.selectAudio.value = preferTitulo;
    }

    onAudioSelected();
    logSystem(`Catálogo cargado: ${audios.length} audio(s) de tipo "${tipo}".`);
  } catch (err) {
    logSystem(`Error cargando catálogo: ${err.message}`);
  }
}

function onAudioSelected() {
  const titulo = el.selectAudio.value;
  if (titulo !== currentAudioId) {
    currentAudioId = titulo;
    updateReactionsRoomUI();
    clearReactionChat();
    if (currentAudioId) {
      sendWsSubscribe(currentAudioId);
    }
  }
  renderSelectedMetadata();
}

// --- Streaming gRPC-Web y Reproductor -------------------------------------
function pedirYReproducir() {
  const opt = el.selectAudio.selectedOptions[0];
  if (!opt || !opt.value) {
    logSystem("Selecciona un audio válido antes de solicitar el stream.");
    return;
  }

  const titulo = opt.value;
  const formato = opt.dataset.formato || "mp3";
  if (currentAudioId !== titulo) {
    currentAudioId = titulo;
    updateReactionsRoomUI();
    clearReactionChat();
  }
  sendWsSubscribe(currentAudioId);

  if (activeStream) {
    activeStream.cancel();
    activeStream = null;
  }

  const req = new AudioRequest();
  req.setTitulo(titulo);
  req.setFormato(formato);

  const chunks = [];
  let chunkCount = 0;
  let totalBytes = 0;

  el.btnReproducir.disabled = true;
  el.playerState.textContent = `Descargando "${titulo}.${formato}" vía gRPC-Web...`;
  logSystem(`Iniciando transmisión gRPC-Web para "${titulo}.${formato}"...`);

  const stream = grpcClient.streamAudio(req, {});
  activeStream = stream;

  stream.on("data", (response) => {
    const bytes = response.getData_asU8();
    chunks.push(bytes);
    chunkCount++;
    totalBytes += bytes.length;
    logSystem(`gRPC-Web chunk #${chunkCount} recibido (${bytes.length} bytes)`);
  });

  stream.on("error", (err) => {
    el.btnReproducir.disabled = false;
    activeStream = null;
    el.playerState.textContent = `Error en stream (${err.message})`;
    logSystem(`Error en transmisión gRPC-Web (${err.code}): ${err.message}`);
  });

  stream.on("end", async () => {
    el.btnReproducir.disabled = false;
    activeStream = null;

    if (totalBytes === 0) {
      el.playerState.textContent = "Stream vacío.";
      logSystem("Transmisión gRPC-Web finalizada sin bytes de audio.");
      return;
    }

    const blob = new Blob(chunks, { type: "audio/mpeg" });
    if (currentObjectUrl) {
      URL.revokeObjectURL(currentObjectUrl);
    }
    currentObjectUrl = URL.createObjectURL(blob);

    el.audioPlayer.src = currentObjectUrl;
    el.audioPlayer.load();
    el.btnPlay.disabled = false;
    el.btnPause.disabled = false;
    el.playerState.textContent = `Audio "${titulo}.${formato}" cargado (${(totalBytes / 1024).toFixed(1)} KB).`;

    logSystem(
      `Transmisión gRPC-Web completada: ${chunkCount} chunks, ${totalBytes} bytes. Blob armado (audio/mpeg).`
    );

    try {
      await el.audioPlayer.play();
    } catch (playErr) {
      logSystem(
        `Presiona "▶ Play" en el reproductor para iniciar la reproducción (${playErr.message}).`
      );
    }

    await registrarReproduccion(titulo);
  });
}

// --- Estadísticas / Historial (aislado por client_id) --------------------
async function registrarReproduccion(titulo) {
  try {
    const res = await fetch(`${API_BASE}/api/estadisticas/reproducir`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        client_id: clientId,
        audio_id: titulo,
        titulo,
      }),
    });
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `HTTP ${res.status}`);
    }
    logSystem(`Estadística actualizada (${userLabel}): ${data.texto}`);
    await cargarHistorial();
  } catch (err) {
    logSystem(`Error actualizando estadísticas: ${err.message}`);
  }
}

async function cargarHistorial() {
  try {
    const res = await fetch(
      `${API_BASE}/api/estadisticas/historial?client_id=${encodeURIComponent(clientId)}`
    );
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `HTTP ${res.status}`);
    }

    const items = data.historial || [];
    el.listaHistorial.innerHTML = "";

    if (items.length === 0) {
      const li = document.createElement("li");
      li.className = "history-empty";
      li.textContent = "Sin reproducciones registradas aún.";
      el.listaHistorial.appendChild(li);
      return;
    }

    for (const item of items) {
      const li = document.createElement("li");
      const strong = document.createElement("strong");
      strong.textContent = item.titulo;
      const sufijo = item.reproducciones === 1 ? "vez" : "veces";
      li.appendChild(strong);
      li.appendChild(document.createTextNode(` - Reproducido ${item.reproducciones} ${sufijo}`));
      el.listaHistorial.appendChild(li);
    }
  } catch (err) {
    logSystem(`Error cargando historial: ${err.message}`);
  }
}

// --- Suscripciones y Pagos ------------------------------------------------
async function pagarSuscripcion() {
  const plan = el.selectPlan.value;
  el.btnPagar.disabled = true;
  try {
    const res = await fetch(`${API_BASE}/api/suscripciones/pagar`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ plan }),
    });
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `HTTP ${res.status}`);
    }
    el.inputToken.value = data.token;
    el.tokenStatus.textContent = `Pago aprobado (${data.plan}). Token listo para validar.`;
    logSystem(`Token generado: ${data.token} (${data.plan})`);
  } catch (err) {
    el.tokenStatus.textContent = `Error en pago: ${err.message}`;
    logSystem(`Error al procesar pago: ${err.message}`);
  } finally {
    el.btnPagar.disabled = false;
  }
}

async function reutilizarToken() {
  const token = el.inputToken.value.trim();
  if (!token) {
    el.tokenStatus.textContent = "Ingresa o genera un token primero.";
    logSystem("Error: Debes ingresar un token para validarlo.");
    return;
  }
  el.btnReutilizarToken.disabled = true;
  try {
    const res = await fetch(`${API_BASE}/api/suscripciones/validar-token`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
    });
    const data = await res.json();
    if (!res.ok || !data.valido) {
      const msg = data.error || `HTTP ${res.status}`;
      el.tokenStatus.textContent = `Error: ${msg}`;
      logSystem(`Error: ${msg} (token: ${token})`);
      return;
    }
    el.tokenStatus.textContent = `Éxito: ${data.mensaje}`;
    logSystem(`Token validado con éxito: ${data.token} (${data.plan})`);
  } catch (err) {
    el.tokenStatus.textContent = `Error validando token: ${err.message}`;
    logSystem(`Error validando token: ${err.message}`);
  } finally {
    el.btnReutilizarToken.disabled = false;
  }
}

// --- Panel de Administrador Dinámico --------------------------------------
function renderAdminMetadataInputs(tipo) {
  const fields = METADATA_SCHEMA[tipo];
  el.adminMetadataFields.innerHTML = "";

  if (!fields || fields.length === 0) {
    el.adminMetadataFields.hidden = true;
    return;
  }

  const title = document.createElement("div");
  title.className = "admin-meta-title";
  title.textContent = `Metadatos para ${tipo}:`;
  el.adminMetadataFields.appendChild(title);

  for (const field of fields) {
    const row = document.createElement("div");
    row.className = "admin-meta-row";

    const label = document.createElement("label");
    label.setAttribute("for", `adminMeta_${field.key}`);
    label.textContent = field.label;

    const input = document.createElement("input");
    input.type = "text";
    input.id = `adminMeta_${field.key}`;
    input.dataset.metaKey = field.key;
    input.placeholder = field.placeholder;

    row.appendChild(label);
    row.appendChild(input);
    el.adminMetadataFields.appendChild(row);
  }

  el.adminMetadataFields.hidden = false;
}

async function registrarYSubirAudio() {
  const file = el.inputArchivo.files[0];
  const tipo = el.adminTipoAudio.value;
  if (!file) {
    logSystem("Admin: Selecciona un archivo de audio antes de registrar.");
    return;
  }
  if (!tipo) {
    logSystem("Admin: Selecciona el Tipo de Audio antes de registrar.");
    return;
  }

  const dotIdx = file.name.lastIndexOf(".");
  const titulo = dotIdx > 0 ? file.name.slice(0, dotIdx) : file.name;
  const formato = dotIdx > 0 ? file.name.slice(dotIdx + 1).toLowerCase() : "mp3";

  const metadatos = {};
  const metaInputs = el.adminMetadataFields.querySelectorAll("input[data-meta-key]");
  for (const input of metaInputs) {
    const val = input.value.trim();
    if (val) {
      metadatos[input.dataset.metaKey] = val;
    }
  }

  const formData = new FormData();
  formData.append("titulo", titulo);
  formData.append("tipo", tipo);
  formData.append("formato", formato);
  formData.append("archivo", file.name);
  formData.append("metadatos", JSON.stringify(metadatos));
  formData.append("archivo_binario", file);

  el.btnRegistrarAudio.disabled = true;
  try {
    const res = await fetch(`${API_BASE}/api/audios/registrar`, {
      method: "POST",
      body: formData,
    });
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `HTTP ${res.status}`);
    }

    logSystem(
      `Admin: Audio "${data.audio.titulo}.${data.audio.formato}" (${data.audio.tipo}) guardado y registrado.`
    );

    el.inputArchivo.value = "";
    for (const input of metaInputs) {
      input.value = "";
    }

    el.tipoAudio.value = tipo;
    await cargarCatalogo(tipo, data.audio.titulo);
  } catch (err) {
    logSystem(`Admin: Error registrando audio: ${err.message}`);
  } finally {
    el.btnRegistrarAudio.disabled = false;
  }
}

// --- Eventos de la UI -----------------------------------------------------
el.tipoAudio.addEventListener("change", () => cargarCatalogo(el.tipoAudio.value));
el.selectAudio.addEventListener("change", onAudioSelected);
el.btnReproducir.addEventListener("click", pedirYReproducir);

el.btnPlay.addEventListener("click", () => {
  el.audioPlayer.play().catch((err) => logSystem(`Error al reproducir: ${err.message}`));
});
el.btnPause.addEventListener("click", () => {
  el.audioPlayer.pause();
});

el.audioPlayer.addEventListener("play", () => {
  el.playerState.textContent = `Reproduciendo "${currentAudioId}"`;
  if (sendWsPayload({ type: "play", audio_id: currentAudioId, sender: userLabel })) {
    logSystem(`[WebSocket] Reacción enviada: reprodujo "${currentAudioId}".`);
  }
});

el.audioPlayer.addEventListener("pause", () => {
  if (el.audioPlayer.src) {
    el.playerState.textContent = `En pausa "${currentAudioId}"`;
    if (sendWsPayload({ type: "pause", audio_id: currentAudioId, sender: userLabel })) {
      logSystem(`[WebSocket] Reacción enviada: pausó "${currentAudioId}".`);
    }
  }
});

el.audioPlayer.addEventListener("ended", () => {
  el.playerState.textContent = `Finalizado "${currentAudioId}"`;
});

el.audioPlayer.addEventListener("error", () => {
  const errCode = el.audioPlayer.error ? el.audioPlayer.error.code : "desconocido";
  logSystem(`Error en elemento <audio> (código ${errCode}). Verifica que el archivo MP3 sea válido.`);
});

el.reactionsBar.addEventListener("click", (event) => {
  const btn = event.target.closest(".reaction-btn");
  if (!btn || !currentAudioId) return;

  const emoji = btn.dataset.emoji;
  const icon = btn.dataset.icon;

  spawnFloatingEmoji(btn, icon, emoji);
  appendReactionChat(`Tú (${userLabel}) reaccionaste con ${emoji}`);

  if (
    sendWsPayload({
      type: "reaction",
      audio_id: currentAudioId,
      emoji,
      icon,
      sender: userLabel,
    })
  ) {
    logSystem(`Reacción enviada a "${currentAudioId}": ${emoji}`);
  }
});

el.btnPagar.addEventListener("click", pagarSuscripcion);
el.btnReutilizarToken.addEventListener("click", reutilizarToken);
el.adminTipoAudio.addEventListener("change", () =>
  renderAdminMetadataInputs(el.adminTipoAudio.value)
);
el.btnRegistrarAudio.addEventListener("click", registrarYSubirAudio);

// --- Inicialización -------------------------------------------------------
window.addEventListener("DOMContentLoaded", async () => {
  el.historyUserBadge.textContent = `ID: ${clientId.slice(0, 8)}`;
  updateReactionsRoomUI();
  clearReactionChat();
  logSystem(`Aplicación iniciada (${userLabel}). Gateway: ${API_BASE}`);
  await cargarCatalogo(el.tipoAudio.value);
  await cargarHistorial();
  connectWebSocket();
});

const $ = (id) => document.getElementById(id);
const state = { events: [], socket: null };

function encodeToken(value) {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

function setStatus(text, online) {
  $("status").textContent = text;
  $("status").className = `status ${online ? "online" : "offline"}`;
}

function render() {
  const filter = $("filter").value;
  const visible = filter ? state.events.filter((event) => event.type === filter) : state.events;
  $("total").textContent = state.events.length;
  $("critical").textContent = state.events.filter((event) => event.severity === "critical").length;
  $("last").textContent = state.events.length ? new Date(state.events[0].timestamp).toLocaleTimeString() : "—";
  $("events").replaceChildren(...visible.map((event) => {
    const item = document.createElement("li");
    item.className = `event ${event.severity}`;
    const heading = document.createElement("strong");
    heading.textContent = `${event.type} · ${event.source}`;
    const message = document.createElement("p");
    message.textContent = event.message;
    const time = document.createElement("time");
    time.textContent = new Date(event.timestamp).toLocaleString();
    item.append(heading, message, time);
    return item;
  }));
  const types = [...new Set(state.events.map((event) => event.type))].sort();
  const selected = $("filter").value;
  $("filter").replaceChildren(new Option("All event types", ""), ...types.map((type) => new Option(type, type)));
  $("filter").value = selected;
}

function add(events) {
  state.events.unshift(...events);
  state.events = state.events.slice(0, 1000);
  render();
}

$("connect").addEventListener("click", () => {
  if (state.socket) state.socket.close();
  const endpoint = $("endpoint").value.trim();
  const token = $("token").value;
  if (!endpoint || !token) {
    setStatus("endpoint + token required", false);
    return;
  }
  const protocol = `ion-bearer.${encodeToken(token)}`;
  const socket = new WebSocket(endpoint, protocol);
  state.socket = socket;
  setStatus("connecting", false);
  socket.addEventListener("open", () => setStatus("live", true));
  socket.addEventListener("close", () => setStatus("offline", false));
  socket.addEventListener("error", () => setStatus("connection error", false));
  socket.addEventListener("message", (message) => {
    const frame = JSON.parse(message.data);
    if (frame.type === "snapshot") add(frame.events || []);
    if (frame.type === "event" && frame.event) add([frame.event]);
  });
});

$("filter").addEventListener("change", render);

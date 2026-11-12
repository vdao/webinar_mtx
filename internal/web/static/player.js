/* Minimal WHEP client for the webinar player.
 * Connects to the same-origin proxy /whep/{slug}, which authenticates the
 * viewer session and forwards signaling to MediaMTX. Media (WebRTC) flows
 * directly between the browser and the media server. */
(function () {
  "use strict";

  var video = document.getElementById("video");
  var overlay = document.getElementById("player-overlay");
  var msg = document.getElementById("player-msg");
  var spinner = document.getElementById("player-spinner");
  var statusPill = document.getElementById("stream-status");
  var muteBtn = document.getElementById("btn-mute");

  var pc = null;
  var stopped = false;
  var retryDelay = 2000;
  var maxRetryDelay = 15000;

  function setStatus(state, text) {
    if (statusPill) {
      statusPill.textContent = text;
      statusPill.className = "badge badge-live " +
        (state === "live" ? "" : "is-wait");
    }
  }

  function showMessage(text, spin) {
    if (overlay) {
      overlay.classList.remove("hidden");
      msg.textContent = text;
      spinner.style.display = spin ? "" : "none";
    }
  }

  function hideMessage() {
    if (overlay) overlay.classList.add("hidden");
  }

  function waitIceGathering(pc, timeoutMs) {
    return new Promise(function (resolve) {
      if (pc.iceGatheringState === "complete") return resolve();
      var timer = setTimeout(finish, timeoutMs || 1500);
      function finish() {
        clearTimeout(timer);
        pc.removeEventListener("icegatheringstatechange", onChange);
        resolve();
      }
      function onChange() {
        if (pc.iceGatheringState === "complete") finish();
      }
      pc.addEventListener("icegatheringstatechange", onChange);
    });
  }

  async function connect() {
    if (stopped) return;
    setStatus("wait", "Подключение…");
    showMessage("Подключение к эфиру…", true);

    try {
      pc = new RTCPeerConnection();
      pc.addTransceiver("video", { direction: "recvonly" });
      pc.addTransceiver("audio", { direction: "recvonly" });

      pc.ontrack = function (ev) {
        if (video.srcObject !== ev.streams[0]) {
          video.srcObject = ev.streams[0];
        }
        video.play().catch(function () {
          // autoplay policy: start muted, offer unmute
          video.muted = true;
          video.play().catch(function () {});
          if (muteBtn && !video.muted) muteBtn.hidden = false;
        });
      };

      pc.onconnectionstatechange = function () {
        if (pc.connectionState === "connected") {
          retryDelay = 2000;
          setStatus("live", "В эфире");
          hideMessage();
          if (muteBtn && video.muted) muteBtn.hidden = false;
        } else if (pc.connectionState === "failed" || pc.connectionState === "disconnected") {
          scheduleReconnect("Соединение потеряно, переподключаемся…");
        }
      };

      var offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      await waitIceGathering(pc, 1500);

      var resp = await fetch(window.WHEP_URL, {
        method: "POST",
        headers: { "Content-Type": "application/sdp" },
        body: pc.localDescription.sdp,
      });

      if (!resp.ok) {
        teardown();
        if (resp.status === 401) {
          setStatus("wait", "Сессия истекла");
          showMessage("Сессия истекла. Обновите страницу.", false);
          return;
        }
        if (resp.status === 410) {
          setStatus("wait", "Вебинар завершён");
          showMessage("Вебинар завершён.", false);
          return;
        }
        if (resp.status === 404) {
          setStatus("wait", "Ждём начала эфира");
          showMessage("Эфир ещё не начался. Как только ведущий начнёт трансляцию, видео появится автоматически.", true);
          scheduleRetry();
          return;
        }
        scheduleReconnect("Сервер недоступен, повторяем попытку…");
        return;
      }

      var answer = await resp.text();
      await pc.setRemoteDescription({ type: "answer", sdp: answer });
    } catch (err) {
      teardown();
      scheduleReconnect("Ошибка подключения, повторяем попытку…");
    }
  }

  function teardown() {
    if (pc) {
      pc.ontrack = null;
      pc.onconnectionstatechange = null;
      try { pc.close(); } catch (e) {}
      pc = null;
    }
  }

  function scheduleReconnect(text) {
    teardown();
    setStatus("wait", "Переподключение…");
    showMessage(text || "Переподключаемся…", true);
    scheduleRetry();
  }

  function scheduleRetry() {
    if (stopped) return;
    setTimeout(connect, retryDelay);
    retryDelay = Math.min(retryDelay * 1.5, maxRetryDelay);
  }

  if (window.WEBINAR_STATUS === "ended") {
    setStatus("wait", "Вебинар завершён");
    showMessage("Вебинар завершён. Спасибо, что были с нами!", false);
    return;
  }

  if (muteBtn) {
    muteBtn.addEventListener("click", function () {
      video.muted = false;
      video.play().catch(function () {});
      muteBtn.hidden = true;
    });
  }

  window.addEventListener("pagehide", function () { stopped = true; teardown(); });

  connect();
})();

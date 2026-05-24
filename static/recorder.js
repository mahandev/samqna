(() => {
  const btn = document.getElementById('rec-toggle');
  const preview = document.getElementById('rec-preview');
  const timer = document.getElementById('rec-timer');
  const form = document.getElementById('upload-form');
  if (!btn || !preview) return;

  let stream, recorder, chunks = [], startedAt = 0, tickHandle, stopHandle;

  async function start() {
    chunks = [];
    stream = await navigator.mediaDevices.getUserMedia({ video: { width: 720 }, audio: true });
    preview.srcObject = stream;
    const mime = MediaRecorder.isTypeSupported('video/webm;codecs=vp9,opus') ? 'video/webm;codecs=vp9,opus' : 'video/mp4';
    recorder = new MediaRecorder(stream, { mimeType: mime });
    recorder.ondataavailable = (e) => { if (e.data.size) chunks.push(e.data); };
    recorder.onstop = onStop;
    recorder.start();
    btn.textContent = '■ Stop';
    startedAt = Date.now();
    tick();
    tickHandle = setInterval(tick, 200);
    stopHandle = setTimeout(stop, 60000);
  }
  function tick() {
    const s = Math.floor((Date.now() - startedAt) / 1000);
    timer.textContent = `${s}s / 60s`;
  }
  function stop() {
    if (recorder && recorder.state === 'recording') recorder.stop();
    clearInterval(tickHandle); clearTimeout(stopHandle);
    if (stream) stream.getTracks().forEach(t => t.stop());
    btn.textContent = '● Record';
  }
  function onStop() {
    const ext = recorder.mimeType.includes('webm') ? '.webm' : '.mp4';
    const blob = new Blob(chunks, { type: recorder.mimeType });
    const file = new File([blob], `recording${ext}`, { type: recorder.mimeType });
    const dt = new DataTransfer();
    dt.items.add(file);
    form.video.files = dt.files;
  }
  btn.addEventListener('click', () => recorder && recorder.state === 'recording' ? stop() : start());
})();

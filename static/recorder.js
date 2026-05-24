(() => {
  const btn = document.getElementById('rec-toggle');
  const preview = document.getElementById('rec-preview');
  const timer = document.getElementById('rec-timer');
  const form = document.getElementById('upload-form');
  const submitBtn = document.getElementById('submit-btn');
  const fileInput = form?.querySelector('input[type=file][name=video]');
  const progress = document.getElementById('upload-progress');
  const progressBar = document.getElementById('upload-bar');
  const progressLabel = document.getElementById('upload-label');
  if (!form) return;

  let stream, recorder, chunks = [], startedAt = 0, tickHandle, stopHandle;

  async function startRecording() {
    chunks = [];
    try {
      stream = await navigator.mediaDevices.getUserMedia({ video: { width: 720 }, audio: true });
    } catch (err) {
      alert('Could not access camera/microphone: ' + err.message);
      return;
    }
    preview.srcObject = stream;
    const mime = MediaRecorder.isTypeSupported('video/webm;codecs=vp9,opus')
      ? 'video/webm;codecs=vp9,opus'
      : (MediaRecorder.isTypeSupported('video/webm') ? 'video/webm' : 'video/mp4');
    recorder = new MediaRecorder(stream, { mimeType: mime });
    recorder.ondataavailable = (e) => { if (e.data.size) chunks.push(e.data); };
    recorder.onstop = onRecorderStop;
    recorder.start();
    btn.textContent = '■ Stop';
    btn.classList.remove('btn-error');
    btn.classList.add('btn-warning');
    startedAt = Date.now();
    tickTime();
    tickHandle = setInterval(tickTime, 200);
    stopHandle = setTimeout(stopRecording, 60000);
  }

  function tickTime() {
    const s = Math.floor((Date.now() - startedAt) / 1000);
    timer.textContent = `${s}s / 60s`;
  }

  function stopRecording() {
    if (recorder && recorder.state === 'recording') recorder.stop();
    clearInterval(tickHandle);
    clearTimeout(stopHandle);
    if (stream) stream.getTracks().forEach(t => t.stop());
    btn.textContent = '● Record';
    btn.classList.add('btn-error');
    btn.classList.remove('btn-warning');
  }

  function onRecorderStop() {
    const ext = recorder.mimeType.includes('webm') ? '.webm' : '.mp4';
    const blob = new Blob(chunks, { type: recorder.mimeType });
    const file = new File([blob], `recording${ext}`, { type: recorder.mimeType });
    if (fileInput) {
      const dt = new DataTransfer();
      dt.items.add(file);
      fileInput.files = dt.files;
    }
    // Auto-submit once we have the blob — the user already hit "Stop", their intent is clear.
    if (form.checkValidity()) {
      submitViaXHR();
    } else {
      // Fallback: highlight invalid fields (e.g. missing consent) and let the user fix + click Submit.
      form.reportValidity();
    }
  }

  btn?.addEventListener('click', () => {
    if (recorder && recorder.state === 'recording') stopRecording();
    else startRecording();
  });

  // Intercept regular form submit so we get progress + JSON redirect.
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    if (!form.checkValidity()) { form.reportValidity(); return; }
    submitViaXHR();
  });

  function submitViaXHR() {
    const fd = new FormData(form);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', form.action || '/submit');
    xhr.setRequestHeader('Accept', 'application/json');
    progress?.classList.remove('hidden');
    submitBtn?.setAttribute('disabled', 'disabled');
    btn?.setAttribute('disabled', 'disabled');

    xhr.upload.onprogress = (e) => {
      if (!e.lengthComputable || !progressBar) return;
      const pct = Math.round((e.loaded / e.total) * 100);
      progressBar.value = pct;
      if (progressLabel) progressLabel.textContent = `Uploading… ${pct}%`;
    };

    xhr.upload.onload = () => {
      if (progressLabel) progressLabel.textContent = 'Processing… (server is receiving)';
      if (progressBar) progressBar.removeAttribute('value');
    };

    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        let target = '/';
        try {
          const body = JSON.parse(xhr.responseText);
          target = body.redirect || '/';
        } catch (_) { /* server returned non-JSON, fall back */ }
        location.href = target;
        return;
      }
      handleFailure(xhr.status === 413
        ? 'Video too large (max 50 MB).'
        : xhr.status === 429
        ? 'Daily submission limit reached. Try again tomorrow.'
        : `Upload failed (HTTP ${xhr.status}).`);
    };
    xhr.onerror = () => handleFailure('Network error during upload.');
    xhr.send(fd);
  }

  function handleFailure(msg) {
    if (progressLabel) progressLabel.textContent = msg;
    if (progressBar) { progressBar.value = 0; progressBar.classList.remove('progress-primary'); progressBar.classList.add('progress-error'); }
    submitBtn?.removeAttribute('disabled');
    btn?.removeAttribute('disabled');
  }
})();

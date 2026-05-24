(() => {
  const go = document.getElementById('trim-go');
  if (!go) return;
  go.addEventListener('click', async () => {
    const id = go.dataset.id;
    const start = parseFloat(document.getElementById('trim-start').value);
    const end = parseFloat(document.getElementById('trim-end').value);
    if (end <= start) { alert('End must be after start'); return; }
    const r = await fetch(`/v/${id}/export/trim`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ start, end })
    });
    if (!r.ok) { alert('Trim failed'); return; }
    const blob = await r.blob();
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `clip-${id}.mp4`;
    a.click();
  });
})();

// Chart bootstrap for the org dashboard. Data comes inline via a JSON script
// tag so templates stay server-rendered (no fetch round-trip).
document.addEventListener('DOMContentLoaded', () => {
  const el = document.getElementById('chart-data');
  if (!el || typeof Chart === 'undefined') return;
  let data;
  try { data = JSON.parse(el.textContent); } catch { return; }

  const cost = document.getElementById('costChart');
  if (cost && data.labels) {
    new Chart(cost, {
      type: 'line',
      data: {
        labels: data.labels,
        datasets: [{ label: 'USD/day', data: data.values, borderColor: '#0f172a', tension: 0.3, fill: false }],
      },
      options: { plugins: { legend: { display: false } }, scales: { y: { beginAtZero: true } } },
    });
  }

  const grade = document.getElementById('gradeChart');
  if (grade && data.dist) {
    const letters = ['A', 'B', 'C', 'D', 'F'];
    new Chart(grade, {
      type: 'bar',
      data: {
        labels: letters,
        datasets: [{
          data: letters.map(l => data.dist[l] || 0),
          backgroundColor: ['#16a34a', '#65a30d', '#f59e0b', '#f97316', '#dc2626'],
        }],
      },
      options: { plugins: { legend: { display: false } }, scales: { y: { beginAtZero: true, ticks: { precision: 0 } } } },
    });
  }
});

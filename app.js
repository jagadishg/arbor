// Landing-page interactions: asciinema players, tab switching, copy buttons.
(function () {
  var opts = { theme: 'asciinema', fit: 'width', terminalFontSize: '14px', idleTimeLimit: 2, poster: 'npt:0:02' };
  var players = {};
  function ensure(tab) {
    if (players[tab]) return;
    players[tab] = AsciinemaPlayer.create('./demo/' + tab + '.cast', document.getElementById('player-' + tab), opts);
  }
  ensure('tui');

  document.querySelectorAll('.tab').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var tab = btn.dataset.tab;
      document.querySelectorAll('.tab').forEach(function (b) { b.classList.toggle('active', b === btn); });
      document.querySelectorAll('.player').forEach(function (p) {
        p.classList.toggle('hidden', p.id !== 'player-' + tab);
      });
      ensure(tab);
    });
  });

  document.querySelectorAll('.copy').forEach(function (btn) {
    btn.addEventListener('click', function () {
      navigator.clipboard.writeText(btn.dataset.copy).then(function () {
        var old = btn.textContent; btn.textContent = 'copied!';
        setTimeout(function () { btn.textContent = old; }, 1400);
      });
    });
  });
})();

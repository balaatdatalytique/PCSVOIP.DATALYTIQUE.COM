// PCS VoIP Admin — small UX helpers (~80 lines)

(function () {
  // Simple delete confirmation for forms with data-confirm.
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (form.dataset && form.dataset.confirm) {
      if (!window.confirm(form.dataset.confirm)) {
        e.preventDefault();
      }
    }
  });

  // CSRF helper for fetch requests.
  function csrfToken() {
    var meta = document.querySelector('meta[name="csrf-token"]');
    return meta ? meta.content : '';
  }

  // Bot test panel.
  var botTestForm = document.getElementById('botTestForm');
  if (botTestForm) {
    botTestForm.addEventListener('submit', async function (e) {
      e.preventDefault();
      var msg = document.getElementById('botTestMessage').value.trim();
      var reply = document.getElementById('botTestReply');
      if (!msg) return;
      reply.classList.remove('error');
      reply.textContent = 'Sending…';
      try {
        var fd = new FormData();
        fd.append('message', msg);
        fd.append('_csrf', csrfToken());
        var res = await fetch('/admin/bot/test', {
          method: 'POST',
          body: fd,
          headers: { 'X-CSRF-Token': csrfToken() },
        });
        var data = await res.json();
        if (data.error) {
          reply.classList.add('error');
          reply.textContent = 'Error: ' + data.error;
        } else {
          reply.textContent = data.message || JSON.stringify(data);
        }
      } catch (err) {
        reply.classList.add('error');
        reply.textContent = 'Request failed: ' + err.message;
      }
    });
  }

  // SMTP test button.
  var smtpBtn = document.getElementById('testSmtpBtn');
  if (smtpBtn) {
    smtpBtn.addEventListener('click', async function () {
      var out = document.getElementById('smtpTestResult');
      out.textContent = 'Sending…';
      out.style.color = '';
      try {
        var fd = new FormData();
        fd.append('_csrf', csrfToken());
        var res = await fetch('/admin/settings/test-smtp', {
          method: 'POST',
          body: fd,
          headers: { 'X-CSRF-Token': csrfToken() },
        });
        var data = await res.json();
        if (data.error) {
          out.textContent = 'Failed: ' + data.error;
          out.style.color = '#b02334';
        } else {
          out.textContent = data.ok || 'OK';
          out.style.color = '#4a9633';
        }
      } catch (err) {
        out.textContent = 'Request failed: ' + err.message;
        out.style.color = '#b02334';
      }
    });
  }
})();

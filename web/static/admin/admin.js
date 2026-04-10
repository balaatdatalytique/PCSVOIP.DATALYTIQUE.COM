// PCS VoIP Admin — UX helpers

(function () {
  // ===================== Toast notifications =====================
  // Usage: pcsToast('Document saved.', 'success')
  //        pcsToast('Something went wrong.', 'error')
  window.pcsToast = function (msg, type) {
    type = type || 'success';
    var icons = { success: 'fa-circle-check', error: 'fa-circle-xmark', info: 'fa-circle-info' };
    var wrap = document.getElementById('pcs-toast-wrap');
    if (!wrap) return;
    var el = document.createElement('div');
    el.className = 'pcs-toast ' + type;
    el.innerHTML =
      '<i class="fas ' + (icons[type] || icons.info) + '"></i>' +
      '<span class="toast-msg">' + msg + '</span>' +
      '<button class="toast-close" aria-label="Close">&times;</button>';
    wrap.appendChild(el);
    // trigger reflow, then animate in
    el.offsetHeight;
    el.classList.add('show');
    var dismiss = function () {
      el.classList.remove('show');
      el.classList.add('hide');
      setTimeout(function () { el.remove(); }, 400);
    };
    el.querySelector('.toast-close').addEventListener('click', dismiss);
    setTimeout(dismiss, 4000);
  };

  // Auto-show toast from URL params (?toast=Message&ttype=success)
  (function () {
    var params = new URLSearchParams(window.location.search);
    var msg = params.get('toast');
    if (msg) {
      var type = params.get('ttype') || 'success';
      // Clean URL without reloading
      params.delete('toast');
      params.delete('ttype');
      var clean = window.location.pathname;
      var remaining = params.toString();
      if (remaining) clean += '?' + remaining;
      clean += window.location.hash;
      window.history.replaceState(null, '', clean);
      setTimeout(function () { pcsToast(msg, type); }, 150);
    }
    // Legacy support: ?saved=1
    if (params.get('saved') === '1') {
      params.delete('saved');
      var clean2 = window.location.pathname;
      var remaining2 = params.toString();
      if (remaining2) clean2 += '?' + remaining2;
      clean2 += window.location.hash;
      window.history.replaceState(null, '', clean2);
      setTimeout(function () { pcsToast('Changes saved successfully.', 'success'); }, 150);
    }
  })();

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
          pcsToast('Bot test failed: ' + data.error, 'error');
        } else {
          reply.textContent = data.message || JSON.stringify(data);
          pcsToast('Bot replied successfully.', 'success');
        }
      } catch (err) {
        reply.classList.add('error');
        reply.textContent = 'Request failed: ' + err.message;
        pcsToast('Bot test request failed.', 'error');
      }
    });
  }

  // System prompt: load from local file into the textarea.
  var sysPromptFile = document.getElementById('sysPromptFile');
  if (sysPromptFile) {
    sysPromptFile.addEventListener('change', function () {
      var f = sysPromptFile.files && sysPromptFile.files[0];
      if (!f) return;
      if (f.size > 1024 * 1024) {
        alert('File too large (max 1 MB)');
        sysPromptFile.value = '';
        return;
      }
      var reader = new FileReader();
      reader.onload = function () {
        var ta = document.getElementById('sysPromptArea');
        if (ta) {
          ta.value = String(reader.result || '').trim();
          ta.focus();
        }
      };
      reader.onerror = function () {
        alert('Failed to read file');
      };
      reader.readAsText(f);
      sysPromptFile.value = '';
    });
  }

  // Voice selector: real <select> with an "Other (custom)" option that
  // reveals a free-form text input. The hidden #voiceField carries whatever
  // ends up being submitted.
  var voiceSelect = document.getElementById('voiceSelect');
  var voiceField = document.getElementById('voiceField');
  var voiceCustom = document.getElementById('voiceCustom');
  if (voiceSelect && voiceField && voiceCustom) {
    var initial = (voiceField.value || 'ara').trim();
    var optionExists = false;
    for (var i = 0; i < voiceSelect.options.length; i++) {
      if (voiceSelect.options[i].value === initial) {
        optionExists = true;
        break;
      }
    }
    if (optionExists) {
      voiceSelect.value = initial;
      voiceCustom.classList.add('d-none');
    } else if (initial) {
      voiceSelect.value = '__custom__';
      voiceCustom.classList.remove('d-none');
      voiceCustom.value = initial;
    }
    voiceSelect.addEventListener('change', function () {
      if (voiceSelect.value === '__custom__') {
        voiceCustom.classList.remove('d-none');
        voiceCustom.focus();
        voiceField.value = voiceCustom.value.trim();
      } else {
        voiceCustom.classList.add('d-none');
        voiceField.value = voiceSelect.value;
      }
    });
    voiceCustom.addEventListener('input', function () {
      voiceField.value = voiceCustom.value.trim();
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
          pcsToast('SMTP test failed: ' + data.error, 'error');
        } else {
          out.textContent = data.ok || 'OK';
          out.style.color = '#4a9633';
          pcsToast('Test email sent successfully.', 'success');
        }
      } catch (err) {
        out.textContent = 'Request failed: ' + err.message;
        out.style.color = '#b02334';
        pcsToast('SMTP test request failed.', 'error');
      }
    });
  }
})();

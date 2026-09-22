// auth.js —— 登录弹窗的全部逻辑，取代浏览器原生的 Basic Auth 弹窗。
// app.js 请求 /data/ 拿到 401 时会调用 window.trynetAuth.requireLogin，
// 弹窗登录成功后再回调重新拉取列表。必须排在 app.js 之前加载：app.js 的
// IIFE 一执行完就会立刻发第一个 /data/ 请求，trynetAuth 得提前挂好。
(function () {
  'use strict';

  var overlay = document.getElementById('auth-overlay');
  var form = document.getElementById('auth-form');
  var usernameInput = document.getElementById('auth-username');
  var passwordInput = document.getElementById('auth-password');
  var errorEl = document.getElementById('auth-error');
  var submitBtn = document.getElementById('auth-submit');

  var pendingCallback = null;
  var submitting = false;

  // 弹窗出现时锁定背景滚动，关闭时还原
  function lockScroll() {
    document.body.style.overflow = 'hidden';
  }
  function unlockScroll() {
    document.body.style.overflow = '';
  }

  function showModal() {
    overlay.hidden = false;
    overlay.setAttribute('aria-hidden', 'false');
    lockScroll();
    // 密码框 autofocus：用户名已经预填 admin，省一次点击；弹窗是运行时才
    // 显示出来的（原本带 hidden 属性），HTML 的 autofocus 属性这时不生效，
    // 只能手动 focus。
    window.setTimeout(function () { passwordInput.focus(); }, 0);
  }

  function hideModal() {
    overlay.hidden = true;
    overlay.setAttribute('aria-hidden', 'true');
    unlockScroll();
  }

  function setError(message) {
    if (message) {
      errorEl.textContent = message;
      errorEl.hidden = false;
    } else {
      errorEl.hidden = true;
      errorEl.textContent = '';
    }
  }

  function setSubmitting(state) {
    submitting = state;
    submitBtn.disabled = state;
    submitBtn.textContent = state ? 'Signing in…' : 'Sign in';
  }

  // requireLogin 弹出登录框；成功登录后调用 onSuccess 让调用方重新发起
  // 原本被 401 挡下的那次请求。弹窗已经开着的时候重复调用只是替换回调，
  // 不会再弹一次。
  function requireLogin(onSuccess) {
    pendingCallback = onSuccess;
    setError(null);
    passwordInput.value = '';
    if (overlay.hidden) showModal();
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    setError(null);

    fetch('/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: usernameInput.value, password: passwordInput.value })
    }).then(function (resp) {
      setSubmitting(false);
      if (!resp.ok) {
        // 服务端本来就不区分用户名错还是密码错，这里原样照搬文案，不额外猜
        setError('Incorrect user name or password');
        passwordInput.value = '';
        passwordInput.focus();
        return;
      }
      hideModal();
      var callback = pendingCallback;
      pendingCallback = null;
      if (callback) callback();
    }).catch(function () {
      setSubmitting(false);
      setError('Incorrect user name or password');
    });
  });

  window.trynetAuth = { requireLogin: requireLogin };
})();

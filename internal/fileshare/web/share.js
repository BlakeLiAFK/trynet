// share.js —— 单文件分享链接的前端部分。app.js 在文件行上点分享时调
// window.trynetShare.request，这里负责找服务端签一张 token 并把链接摆出来。
//
// 和 auth.js 一样排在 app.js 之前加载：app.js 渲染列表时就会给按钮绑上
// window.trynetShare.request，挂晚了第一批按钮就是哑的。
(function () {
  'use strict';

  var shareToast = document.getElementById('share-toast');
  var shareToastTitle = document.getElementById('share-toast-title');
  var shareToastUrl = document.getElementById('share-toast-url');
  var shareToastClose = document.getElementById('share-toast-close');

  // ---------------------------------------------------------------
  // 分享链接：找服务端为这一个文件签一张有期限的 token，然后把完整 URL
  // 摆出来。token 只对这个文件有效、只读、一小时过期，跟登录密码不是
  // 一回事——服务端的约束见 share.go。
  // ---------------------------------------------------------------

  function request(dataPath, button) {
    button.disabled = true;
    fetch('/auth/share', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: dataPath })
    }).then(function (resp) {
      button.disabled = false;
      if (resp.status === 401) {
        if (window.trynetAuth) {
          window.trynetAuth.requireLogin(function () { request(dataPath, button); });
        }
        return null;
      }
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      return resp.json();
    }).then(function (data) {
      if (!data) return;
      showShareToast(location.origin + data.url, data.expiresIn);
    }).catch(function () {
      button.disabled = false;
      showShareToast(null, 0);
    });
  }

  // 提示条里始终放完整链接的只读 input 并自动全选，同时静默试一次剪贴板。
  // 不分"复制成功/失败"两条路径：剪贴板 API 在非安全上下文和部分浏览器里
  // 本来就会拒绝，而无论它成不成，用户都看得见链接、都能 Ctrl+C。
  function showShareToast(url, expiresIn) {
    if (!url) {
      shareToastTitle.textContent = 'Could not create a share link';
      shareToastUrl.hidden = true;
      shareToast.hidden = false;
      return;
    }
    shareToastTitle.textContent = 'Share link · expires in ' + formatDuration(expiresIn);
    shareToastUrl.hidden = false;
    shareToastUrl.value = url;
    shareToast.hidden = false;
    shareToastUrl.focus();
    shareToastUrl.select();
    // select() 把光标丢到末尾、顺带把输入框滚到最右边，看到的就成了半截
    // 域名。滚回开头，让人第一眼能认出这是自己那台机器的链接。
    shareToastUrl.scrollLeft = 0;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url).catch(function () {});
    }
  }

  function formatDuration(seconds) {
    if (seconds >= 3600) {
      var hours = Math.round(seconds / 3600);
      return hours + (hours === 1 ? ' hour' : ' hours');
    }
    var mins = Math.max(1, Math.round(seconds / 60));
    return mins + (mins === 1 ? ' minute' : ' minutes');
  }

  shareToastClose.addEventListener('click', function () {
    shareToast.hidden = true;
  });

  // Esc 关掉提示条。链接已经躺在剪贴板里了，不该逼用户去够那个小叉。
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && !shareToast.hidden) {
      shareToast.hidden = true;
    }
  });

  window.trynetShare = { request: request };
})();

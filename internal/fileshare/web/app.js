// app.js —— trynet 文件服务页面的全部交互逻辑，不依赖任何第三方库。
// 页面从 /data/<path> 取目录列表（JSON），往同一个地址 POST 上传文件。
// 上传用 XMLHttpRequest 而不是 fetch：只有 XHR 的 upload.onprogress 能拿到
// 上传进度，fetch 目前做不到这件事。
(function () {
  'use strict';

  var SVG_NS = 'http://www.w3.org/2000/svg';
  var XLINK_NS = 'http://www.w3.org/1999/xlink';

  var brandLink = document.getElementById('brand-link');
  var breadcrumbEl = document.getElementById('breadcrumb');
  var uploadBtn = document.getElementById('upload-btn');
  var dropzone = document.getElementById('dropzone');
  var fileInput = document.getElementById('file-input');
  var uploadsEl = document.getElementById('uploads');
  var listPanel = document.getElementById('list-panel');
  var entriesEl = document.getElementById('entries');
  var emptyEl = document.getElementById('empty-message');
  var dragOverlay = document.getElementById('drag-overlay');
  var dropStrip = document.getElementById('drop-strip');

  var currentPath = '/';
  var uploadEnabled = false;
  var shareEnabled = false;

  // ---------------------------------------------------------------
  // 小工具：格式化 + 图标
  // ---------------------------------------------------------------

  function formatBytes(n) {
    if (n < 1024) return n + ' B';
    var units = ['KB', 'MB', 'GB', 'TB'];
    var value = n;
    var unit = -1;
    do {
      value /= 1024;
      unit += 1;
    } while (value >= 1024 && unit < units.length - 1);
    return value.toFixed(value < 10 ? 1 : 0) + ' ' + units[unit];
  }

  function formatTime(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    return d.toLocaleString();
  }

  // useIcon 引用 index.html 顶部 <symbol> 表里的图标，页面不内嵌任何图标库，
  // 每个 <svg><use> 都指向同一份手写路径
  function useIcon(id, className) {
    var svg = document.createElementNS(SVG_NS, 'svg');
    if (className) svg.setAttribute('class', className);
    svg.setAttribute('aria-hidden', 'true');
    svg.setAttribute('focusable', 'false');
    var use = document.createElementNS(SVG_NS, 'use');
    use.setAttributeNS(XLINK_NS, 'href', '#' + id); // 旧版 Safari 需要 xlink:href
    use.setAttribute('href', '#' + id);
    svg.appendChild(use);
    return svg;
  }

  function makeMono(text) {
    var span = document.createElement('span');
    span.className = 'mono';
    span.textContent = text;
    return span;
  }

  // ---------------------------------------------------------------
  // 面包屑：品牌名之外的路径部分，每一段都能点回那一级；根目录没有上级，
  // 不渲染任何内容
  // ---------------------------------------------------------------

  function renderBreadcrumb(fullPath) {
    breadcrumbEl.textContent = '';
    if (fullPath === '/') return;

    var parts = fullPath.split('/').filter(function (p) { return p !== ''; });
    var accum = '/';
    parts.forEach(function (part, i) {
      var sep = document.createElement('span');
      sep.className = 'crumb-sep';
      sep.textContent = '/';
      breadcrumbEl.appendChild(sep);

      accum += part + '/';
      if (i === parts.length - 1) {
        var current = document.createElement('span');
        current.className = 'crumb-current';
        current.textContent = part;
        breadcrumbEl.appendChild(current);
        return;
      }

      var target = accum;
      var link = document.createElement('a');
      link.className = 'crumb-link';
      link.href = '#';
      link.textContent = part;
      link.addEventListener('click', function (e) {
        e.preventDefault();
        navigate(target);
      });
      breadcrumbEl.appendChild(link);
    });
  }

  // ---------------------------------------------------------------
  // 目录列表：从 /data/<path> 取 JSON 再渲染成 DOM。名字一律用 textContent
  // 写入，不拼 innerHTML——文件名是攻击者可控的，拼字符串就是 XSS。
  // ---------------------------------------------------------------

  function fetchListing(path) {
    fetch('/data' + path)
      .then(function (resp) {
        // 没有会话 cookie（或者 Basic Auth 也没带）时 /data/ 回 401——交给
        // 登录弹窗接管，弹窗登录成功后会再原样调一次 fetchListing。这条分支
        // 不往下走进普通的错误展示逻辑，避免列表区域先闪一下"加载失败"。
        if (resp.status === 401) {
          if (window.trynetAuth) {
            window.trynetAuth.requireLogin(function () { fetchListing(path); });
          }
          return null;
        }
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        return resp.json();
      })
      .then(function (listing) {
        if (!listing) return; // 401 分支已经转交给登录弹窗，这里没有数据可渲染
        currentPath = listing.path;
        renderBreadcrumb(listing.path);
        renderEntries(listing);
      })
      .catch(function (err) {
        listPanel.hidden = false;
        entriesEl.textContent = '';
        var li = document.createElement('li');
        li.className = 'entry';
        li.textContent = 'Failed to load directory listing: ' + err.message;
        entriesEl.appendChild(li);
      });
  }

  function navigate(path) {
    fetchListing(path);
  }

  // 层级：目录为空时大拖放区是页面唯一内容（此时列表面板和它的 ".." 行都不
  // 渲染，返回上级改走面包屑）；目录非空时列表才是主角，上传入口收进
  // 页头的按钮里
  function renderEntries(listing) {
    entriesEl.textContent = '';

    uploadEnabled = listing.uploadEnabled;
    shareEnabled = listing.shareEnabled;
    var isEmpty = listing.entries.length === 0;
    var showBigDropzone = uploadEnabled && isEmpty;
    var grid = window.trynetGrid.isGrid();

    entriesEl.className = grid ? 'entries cards' : 'entries';
    if (!isEmpty) {
      if (listing.path !== '/') {
        entriesEl.appendChild(makeParentRow(listing.path));
      }
      listing.entries.forEach(function (entry) {
        entriesEl.appendChild(grid
          ? window.trynetGrid.makeCard(listing.path, entry, navigate, useIcon)
          : makeEntryRow(listing.path, entry));
      });
    }

    dropzone.hidden = !showBigDropzone;
    // 非空目录也要有个看得见的拖放落点。页头那个按钮太容易被当成装饰，
    // 而整页拖放覆盖层只在拖动开始后才出现——在那之前没有任何东西告诉用户
    // "这里能传文件"
    dropStrip.hidden = !uploadEnabled || isEmpty;
    uploadBtn.hidden = !uploadEnabled || isEmpty;
    listPanel.hidden = isEmpty;
    emptyEl.hidden = !isEmpty || uploadEnabled;
    window.trynetGrid.showToggle(!isEmpty);
  }

  // 切换排布时原地重画当前目录，不重新请求——列表数据没变，变的只是怎么摆
  window.trynetGrid.onChange(function () {
    if (!listPanel.hidden || !dropzone.hidden) fetchListing(currentPath);
  });

  function makeParentRow(path) {
    var li = document.createElement('li');
    li.className = window.trynetGrid.isGrid() ? 'entry parent-in-grid' : 'entry';
    li.appendChild(useIcon('icon-folder', 'entry-icon'));

    var main = document.createElement('a');
    main.className = 'entry-main';
    main.href = '#';
    var name = document.createElement('span');
    name.className = 'entry-name';
    name.textContent = '..';
    main.appendChild(name);
    main.addEventListener('click', function (e) {
      e.preventDefault();
      var trimmed = path.replace(/\/$/, '');
      var parent = trimmed.substring(0, trimmed.lastIndexOf('/') + 1) || '/';
      navigate(parent);
    });
    li.appendChild(main);
    return li;
  }

  function makeEntryRow(basePath, entry) {
    var li = document.createElement('li');
    li.className = 'entry';
    li.appendChild(useIcon(entry.isDir ? 'icon-folder' : 'icon-file', 'entry-icon'));

    var main = document.createElement('a');
    main.className = 'entry-main';

    var name = document.createElement('span');
    name.className = 'entry-name';
    name.textContent = entry.name;
    main.appendChild(name);

    if (entry.isDir) {
      var childPath = basePath + encodeURIComponent(entry.name) + '/';
      main.href = '#';
      main.addEventListener('click', function (e) {
        e.preventDefault();
        navigate(childPath);
      });
      li.appendChild(main);
      return li;
    }

    var size = document.createElement('span');
    size.className = 'entry-size mono';
    size.appendChild(makeMono(formatBytes(entry.size)));
    main.appendChild(size);

    var date = document.createElement('span');
    date.className = 'entry-date';
    date.textContent = formatTime(entry.modTime);
    main.appendChild(date);

    var downloadHref = '/data' + basePath + encodeURIComponent(entry.name);
    main.href = downloadHref;
    li.appendChild(main);

    // 目录行在上面就 return 了，分享按钮只会落在文件上——签发接口本来也拒绝
    // 目录，按钮不该出现在点了必然失败的地方
    if (shareEnabled) {
      var share = document.createElement('button');
      share.type = 'button';
      share.className = 'entry-share';
      share.setAttribute('aria-label', 'Copy a share link for ' + entry.name);
      share.title = 'Copy share link';
      share.appendChild(useIcon('icon-link'));
      share.addEventListener('click', function (e) {
        e.preventDefault();
        window.trynetShare.request(downloadHref, share);
      });
      li.appendChild(share);
    }

    var download = document.createElement('a');
    download.className = 'entry-download';
    download.href = downloadHref;
    download.setAttribute('aria-label', 'Download ' + entry.name);
    download.appendChild(useIcon('icon-download'));
    li.appendChild(download);

    return li;
  }

  // ---------------------------------------------------------------
  // 拖拽 + 点击选择。桌面拖拽落在窗口任意位置都算数（整页覆盖层），
  // 点击靠 dropzone（目录为空时）或页头的 Upload 按钮打开同一个隐藏
  // file input，这条路径手机和桌面都能用。
  // ---------------------------------------------------------------

  var dragCounter = 0;

  function dragCarriesFiles(e) {
    var types = e.dataTransfer && e.dataTransfer.types;
    if (!types) return true;
    return Array.prototype.indexOf.call(types, 'Files') !== -1;
  }

  function showDragOverlay() {
    dragOverlay.classList.add('visible');
  }

  function hideDragOverlay() {
    dragOverlay.classList.remove('visible');
  }

  // dragenter/dragleave 在鼠标从父元素移到子元素、又从子元素移回父元素时
  // 都会各触发一次——用计数器抵消，而不是判断 relatedTarget（覆盖层自己
  // 在拖拽过程中也在增减，relatedTarget 容易判断错）。计数器归零才是
  // 真的离开了窗口。
  document.addEventListener('dragenter', function (e) {
    if (!uploadEnabled || !dragCarriesFiles(e)) return;
    dragCounter += 1;
    showDragOverlay();
  });

  document.addEventListener('dragover', function (e) {
    e.preventDefault(); // 挡住浏览器默认行为：直接打开拖进来的文件
  });

  document.addEventListener('dragleave', function () {
    if (!uploadEnabled) return;
    dragCounter = Math.max(0, dragCounter - 1);
    if (dragCounter === 0) hideDragOverlay();
  });

  document.addEventListener('drop', function (e) {
    e.preventDefault();
    dragCounter = 0;
    hideDragOverlay();
    if (uploadEnabled && e.dataTransfer && e.dataTransfer.files.length) {
      uploadFiles(e.dataTransfer.files);
    }
  });

  // 品牌名同时是"回根目录"的入口——面包屑只列出当前路径往上的那几段，
  // 空目录时列表面板整个不渲染，这是唯一还能点回根目录的地方
  brandLink.addEventListener('click', function (e) {
    e.preventDefault();
    navigate('/');
  });

  uploadBtn.addEventListener('click', function () {
    fileInput.click();
  });

  // label 默认不进 tab 顺序、Enter/Space 也不会触发关联的 input，这里手动补上，
  // 保证两个拖放入口（空目录的大框、非空目录的窄条）在键盘下都能操作
  [dropzone, dropStrip].forEach(function (el) {
    el.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        fileInput.click();
      }
    });
  });

  fileInput.addEventListener('change', function () {
    if (fileInput.files.length) {
      uploadFiles(fileInput.files);
    }
    fileInput.value = '';
  });

  // ---------------------------------------------------------------
  // 上传：一次选/拖多个文件，逐个各自发一个 XHR、各自显示进度。
  // 签名效果是"进度即行本身"：行底边一条 3px 细线从左往右填充，
  // 完成时线淡出、换成一个对勾，不另外放进度条控件。
  // ---------------------------------------------------------------

  function uploadFiles(fileList) {
    uploadsEl.hidden = false;
    Array.prototype.forEach.call(fileList, uploadOneFile);
  }

  function uploadOneFile(file) {
    var row = document.createElement('div');
    row.className = 'upload-row';

    var icon = useIcon('icon-file', 'upload-icon');
    row.appendChild(icon);

    var info = document.createElement('div');
    info.className = 'upload-info';
    var name = document.createElement('span');
    name.className = 'upload-name';
    name.textContent = file.name;
    var sub = document.createElement('span');
    sub.className = 'upload-size mono';
    sub.textContent = formatBytes(file.size);
    info.appendChild(name);
    info.appendChild(sub);
    row.appendChild(info);

    var status = document.createElement('span');
    status.className = 'upload-pct mono';
    status.textContent = '0%';
    row.appendChild(status);

    var progress = document.createElement('span');
    progress.className = 'upload-progress';
    var fill = document.createElement('span');
    fill.className = 'upload-progress-fill';
    progress.appendChild(fill);
    row.appendChild(progress);

    uploadsEl.appendChild(row);

    var form = new FormData();
    form.append('file', file);

    var xhr = new XMLHttpRequest();
    xhr.open('POST', '/data' + currentPath, true);

    xhr.upload.onprogress = function (e) {
      if (!e.lengthComputable) return;
      var pct = Math.round((e.loaded / e.total) * 100);
      fill.style.width = pct + '%';
      status.textContent = pct + '%';
    };

    xhr.onload = function () {
      if (xhr.status >= 200 && xhr.status < 300) {
        fill.style.width = '100%';
        row.classList.add('done');
        status.className = 'upload-pct';
        status.textContent = '';
        status.appendChild(useIcon('icon-check', 'upload-icon done'));
        fetchListing(currentPath);
        window.setTimeout(function () {
          if (row.parentNode) row.parentNode.removeChild(row);
        }, 4000);
      } else {
        failRow(row, icon, sub, status, reasonFromXHR(xhr));
      }
    };

    xhr.onerror = function () {
      failRow(row, icon, sub, status, 'network error');
    };

    xhr.send(form);
  }

  function failRow(row, icon, sub, status, reason) {
    row.classList.add('error');
    if (icon.parentNode) icon.parentNode.replaceChild(useIcon('icon-close', 'upload-icon'), icon);
    sub.className = 'upload-status';
    sub.textContent = 'Upload failed — ' + reason;
    status.textContent = '';
  }

  function reasonFromXHR(xhr) {
    if (xhr.status === 0) return 'network error';
    var text = (xhr.responseText || '').trim();
    return text || ('HTTP ' + xhr.status);
  }

  fetchListing(currentPath);
})();

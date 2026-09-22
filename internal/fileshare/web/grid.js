// grid.js —— 列表 / 网格两种排布的切换和网格卡片的渲染。
//
// app.js 每次渲染目录时问一句 isGrid()，决定是画行还是画卡片；用户点切换时
// 这里回调 app.js 重画。选择存在 localStorage，刷新后保持。
//
// 和 auth.js / share.js 一样排在 app.js 之前加载。
(function () {
  'use strict';

  var STORAGE_KEY = 'trynet.view';
  var toggle = document.getElementById('view-toggle');
  var listBtn = document.getElementById('view-list');
  var gridBtn = document.getElementById('view-grid');

  var mode = readStoredMode();
  var onChangeCallback = null;

  // localStorage 在隐私窗口、以及浏览器设置为禁止站点数据时，连读都会抛异常，
  // 不是返回 null。读写都得包起来，取不到就回落到列表。
  function readStoredMode() {
    try {
      return window.localStorage.getItem(STORAGE_KEY) === 'grid' ? 'grid' : 'list';
    } catch (e) {
      return 'list';
    }
  }

  function storeMode(value) {
    try {
      window.localStorage.setItem(STORAGE_KEY, value);
    } catch (e) {
      // 存不住就只在本次会话里生效，不值得为此打扰用户
    }
  }

  function setMode(value) {
    mode = value === 'grid' ? 'grid' : 'list';
    storeMode(mode);
    listBtn.setAttribute('aria-checked', String(mode === 'list'));
    gridBtn.setAttribute('aria-checked', String(mode === 'grid'));
    listBtn.classList.toggle('active', mode === 'list');
    gridBtn.classList.toggle('active', mode === 'grid');
    if (onChangeCallback) onChangeCallback();
  }

  listBtn.addEventListener('click', function () { setMode('list'); });
  gridBtn.addEventListener('click', function () { setMode('grid'); });

  // makeCard 画一张网格卡片。图片给缩略图，其它给放大的文件类型图标。
  //
  // 要不要请求缩略图看 entry.thumb——服务端给的答案，前端不按扩展名猜。猜的话
  // 两边的支持列表迟早漂移，而且每个非图片条目都会白跑一次 404。
  //
  // entry.thumb 为 true 也只是"值得一试"：服务端只看了扩展名和大小，没有真的
  // 解码，所以坏图、假扩展名仍然会 404，img 的 onerror 兜底还在。
  function makeCard(basePath, entry, navigate, useIcon) {
    var card = document.createElement('li');
    card.className = 'card';

    var link = document.createElement('a');
    link.className = 'card-link';

    var media = document.createElement('div');
    media.className = 'card-media';

    // 先铺一个图标垫底：目录用它当最终样子，文件先拿它占位，缩略图到了再撤掉。
    // 这样加载过程中格子不会是一块空白。
    var icon = useIcon(entry.isDir ? 'icon-folder' : 'icon-file', 'card-icon');
    media.appendChild(icon);
    link.appendChild(media);

    if (entry.isDir) {
      link.href = '#';
      link.addEventListener('click', function (e) {
        e.preventDefault();
        navigate(basePath + encodeURIComponent(entry.name) + '/');
      });
    } else {
      link.href = '/data' + basePath + encodeURIComponent(entry.name);
      if (entry.thumb) {
        var img = document.createElement('img');
        img.className = 'card-thumb';
        img.alt = '';
        img.loading = 'lazy'; // 浏览器原生懒加载，不自己写 IntersectionObserver
        img.addEventListener('load', function () {
          icon.remove();
          img.classList.add('loaded');
        });
        img.addEventListener('error', function () { img.remove(); }); // 图标留着当兜底
        img.src = '/thumb' + basePath + encodeURIComponent(entry.name);
        media.appendChild(img);
      }
    }

    var name = document.createElement('span');
    name.className = 'card-name';
    name.textContent = entry.name;
    name.title = entry.name; // 名字在卡片里会被截断，hover 能看全
    link.appendChild(name);

    card.appendChild(link);
    return card;
  }

  window.trynetGrid = {
    isGrid: function () { return mode === 'grid'; },
    // showToggle 由 app.js 在拿到目录内容后调用：空目录没东西可排，藏起来
    showToggle: function (visible) { toggle.hidden = !visible; },
    onChange: function (cb) { onChangeCallback = cb; },
    makeCard: makeCard
  };

  setMode(mode);
})();

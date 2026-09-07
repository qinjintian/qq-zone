(function () {
  var meta = window.__QQZONE_META__ || {};
  var yearsMap = window.__QQZONE_YEARS__ || {};
  var feed = document.getElementById("feed");
  var yearBox = document.getElementById("years");
  var searchEl = document.getElementById("search");
  var filterEl = document.getElementById("filter");
  var themeBtn = document.getElementById("theme");
  var lightbox = document.getElementById("lightbox");
  var lbImg = document.getElementById("lb-img");

  var state = {
    year: "all",
    q: "",
    filter: "all",
    lb: [],
    lbIdx: 0
  };

  function allPosts() {
    var years = Object.keys(yearsMap).sort().reverse();
    var list = [];
    years.forEach(function (y) {
      (yearsMap[y] || []).forEach(function (p) { list.push(p); });
    });
    list.sort(function (a, b) { return (b.time || 0) - (a.time || 0); });
    return list;
  }

  var posts = allPosts();

  function escapeHtml(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function emoteHtml(s) {
    return String(s == null ? "" : s).replace(/\[em\]e(\d+)\[\/em\]/gi,
      '<img class="emote" src="https://qzonestyle.gtimg.cn/qzone/em/e$1.gif" alt="">');
  }

  function atUinHtml(s) {
    return String(s == null ? "" : s).replace(/@\{uin:(\d+),nick:(.*?),who:\d+,auto:\d+\}/g, function (_, uin, nick) {
      return '<span class="reply-to">回复</span> <span class="mention">' + escapeHtml(nick) + "</span>";
    });
  }

  function richText(html, content) {
    var s = html || escapeHtml(content || "").replace(/\n/g, "<br>");
    return emoteHtml(atUinHtml(s));
  }

  function initial(name) {
    var s = (name || "?").trim();
    return escapeHtml(s.charAt(0) || "?");
  }

  function avatarHtml(person, cls) {
    person = person || {};
    if (person.avatar) {
      return '<img class="' + cls + '" src="' + escapeHtml(person.avatar) + '" alt="">';
    }
    return '<div class="' + cls + '">' + initial(person.name) + "</div>";
  }

  function haystack(p) {
    var parts = [p.content, p.location, p.source, p.share_title];
    if (p.author) parts.push(p.author.name);
    (p.likes || []).forEach(function (x) { parts.push(x && x.name); });
    if (p.repost) parts.push(p.repost.content, p.repost.author && p.repost.author.name);
    (p.comments || []).forEach(function walk(c) {
      parts.push(c.content, c.author && c.author.name);
      (c.replies || []).forEach(walk);
    });
    return parts.join(" ").toLowerCase();
  }

  function hasType(p, type) {
    return (p.media || []).some(function (m) { return m.type === type && m.path; });
  }

  function visiblePosts() {
    var q = state.q.trim().toLowerCase();
    return posts.filter(function (p) {
      if (state.year !== "all") {
        if (yearOf(p) !== state.year) return false;
      }
      if (state.filter === "photo" && !hasType(p, "image")) return false;
      if (state.filter === "video" && !hasType(p, "video")) return false;
      if (q && haystack(p).indexOf(q) === -1) return false;
      return true;
    });
  }

  function yearOf(p) {
    if (!p.time) return "未知";
    var d = new Date((Number(p.time) + 8 * 3600) * 1000);
    return String(d.getUTCFullYear());
  }

  function renderYears() {
    var counts = { all: posts.length };
    posts.forEach(function (p) {
      var y = yearOf(p);
      counts[y] = (counts[y] || 0) + 1;
    });
    var years = Object.keys(counts).filter(function (k) { return k !== "all"; }).sort().reverse();
    var html = '<button type="button" class="year-btn' + (state.year === "all" ? " active" : "") + '" data-year="all">全部 <span>' + counts.all + "</span></button>";
    years.forEach(function (y) {
      html += '<button type="button" class="year-btn' + (state.year === y ? " active" : "") + '" data-year="' + escapeHtml(y) + '">' +
        escapeHtml(y) + ' <span>' + counts[y] + "</span></button>";
    });
    yearBox.innerHTML = html;
  }

  function mediaGrid(media, postId) {
    media = (media || []).filter(function (m) { return m.path || m.type === "voice"; });
    if (!media.length) return "";
    var images = [];
    var html = "";
    media.forEach(function (m, i) {
      if (m.type === "voice" && m.path) {
        html += '<audio class="voice" controls src="' + escapeHtml(m.path) + '"></audio>';
        return;
      }
      if (m.type === "video" && m.path) {
        html += '<video controls preload="metadata" poster="' + escapeHtml(m.poster || "") + '" src="' + escapeHtml(m.path) + '"></video>';
        return;
      }
      if (m.path) {
        images.push(m.path);
        html += '<img src="' + escapeHtml(m.path) + '" alt="" data-gallery="' + escapeHtml(postId) + '" data-idx="' + (images.length - 1) + '">';
      }
    });
    var n = Math.min(images.length + media.filter(function (m) { return m.type === "video" && m.path; }).length, 9);
    var cls = "grid n" + Math.max(1, Math.min(n, 9));
    return '<div class="' + cls + '" data-imgs="' + escapeHtml(JSON.stringify(images)) + '">' + html + "</div>";
  }

  function commentHtml(c) {
    var media = "";
    (c.media || []).forEach(function (m) {
      if (m.path) media += '<img class="mini" src="' + escapeHtml(m.path) + '" alt="">';
    });
    var replies = "";
    if (c.replies && c.replies.length) {
      replies = '<div class="replies">' + c.replies.map(commentHtml).join("") + "</div>";
    }
    return '<div class="comment">' + avatarHtml(c.author, "avatar") +
      '<div class="c-body"><span class="c-name">' + escapeHtml((c.author && c.author.name) || "") + "</span>" +
      (richText(c.html, c.content)) +
      '<span class="c-time">' + escapeHtml(c.time_text || "") + "</span>" + media + replies + "</div></div>";
  }

  function renderPost(p, idx) {
    var pid = "p" + idx;
    var comments = p.comments || [];
    var commentBlock = "";
    if (comments.length) {
      var shown = comments.slice(0, 2).map(commentHtml).join("");
      var rest = comments.length > 2 ? comments.slice(2).map(commentHtml).join("") : "";
      commentBlock = '<div class="comments">' + shown +
        (rest ? '<div class="more" hidden>' + rest + '</div><button type="button" class="toggle" data-more>查看全部 ' + comments.length + " 条评论</button>" : "") +
        "</div>";
    }
    var likeBlock = "";
    var likeCount = p.like_count || 0;
    if (p.likes && p.likes.length) {
      var names = p.likes.map(function (x) { return x.name || x.uin; }).filter(Boolean);
      var extra = likeCount ? " 共" + likeCount + "人觉得很赞" : " 觉得很赞";
      likeBlock = '<div class="likes"><span class="likes-mark">赞</span><span class="likes-text">' +
        names.map(function (n) { return '<span class="like-name">' + escapeHtml(n) + "</span>"; }).join("、") +
        extra + "</span></div>";
    } else if (likeCount) {
      likeBlock = '<div class="likes"><span class="likes-mark">赞</span><span class="likes-text">共' +
        likeCount + "人觉得很赞</span></div>";
    }
    var statsParts = [];
    if (p.visit_count) statsParts.push("浏览" + p.visit_count + "次");
    if (likeCount) statsParts.push("赞(" + likeCount + ")");
    if (p.comment_count) statsParts.push(p.comment_count + " 条评论");
    var stats = statsParts.length
      ? '<div class="stats">' + statsParts.map(function (s) { return "<span>" + escapeHtml(s) + "</span>"; }).join("") + "</div>"
      : "";
    var loc = p.location ? "<span>" + escapeHtml(p.location) + "</span>" : "";
    var src = p.source ? "<span>" + escapeHtml(p.source) + "</span>" : "";
    var when = "";
    if (p.edit_time_text) {
      if (p.time_text) when += "<span>发布于 " + escapeHtml(p.time_text) + "</span>";
      when += "<span>编辑于 " + escapeHtml(p.edit_time_text) + "</span>";
    } else if (p.time_text) {
      when += "<span>" + escapeHtml(p.time_text) + "</span>";
    }
    var share = p.share_title ? '<a class="share-link" href="' + escapeHtml(p.share_url || "#") + '">' + escapeHtml(p.share_title) + "</a>" : "";
    var repost = "";
    if (p.repost) {
      repost = '<div class="repost"><div class="name">' + escapeHtml((p.repost.author && p.repost.author.name) || "原说说") +
        "</div><div class=\"content\">" + richText(p.repost.html, p.repost.content) + "</div>" +
        mediaGrid(p.repost.media, pid + "-rt") + "</div>";
    }
    return '<article class="card" data-id="' + escapeHtml(p.tid || pid) + '">' +
      '<div class="card-head">' + avatarHtml(p.author, "avatar") +
      '<div class="meta"><div class="name">' + escapeHtml((p.author && p.author.name) || "") + "</div>" +
      '<div class="when">' + when + loc + src + "</div></div></div>" +
      '<div class="content">' + richText(p.html, p.content) + "</div>" +
      share + mediaGrid(p.media, pid) + repost + stats + likeBlock +
      commentBlock + "</article>";
  }

  function render() {
    var list = visiblePosts();
    var notice = meta.notice ? '<div class="notice">' + escapeHtml(meta.notice) + "</div>" : "";
    if (!list.length) {
      feed.innerHTML = notice + '<p class="empty">没有符合条件的说说</p>';
      return;
    }
    feed.innerHTML = notice + list.map(renderPost).join("") + '<p class="end">共 ' + list.length + " 条</p>";
  }

  function fillHeader() {
    var name = meta.nickname || "QQ 空间说说";
    document.getElementById("title").textContent = name + " 的说说";
    document.title = name + " 的说说备份";
    var bits = [];
    if (meta.uin) bits.push("QQ " + meta.uin);
    bits.push((meta.total || posts.length) + " 条");
    if (meta.exported_at) bits.push("导出于 " + meta.exported_at);
    document.getElementById("subtitle").textContent = bits.join(" · ");
  }

  function openLightbox(imgs, idx) {
    if (!imgs || !imgs.length) return;
    state.lb = imgs;
    state.lbIdx = idx || 0;
    lbImg.src = imgs[state.lbIdx];
    lightbox.hidden = false;
  }

  function moveLightbox(delta) {
    if (!state.lb.length) return;
    state.lbIdx = (state.lbIdx + delta + state.lb.length) % state.lb.length;
    lbImg.src = state.lb[state.lbIdx];
  }

  yearBox.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-year]");
    if (!btn) return;
    state.year = btn.getAttribute("data-year");
    renderYears();
    render();
    window.scrollTo(0, 0);
  });

  var t = null;
  searchEl.addEventListener("input", function () {
    clearTimeout(t);
    t = setTimeout(function () {
      state.q = searchEl.value;
      render();
    }, 120);
  });
  filterEl.addEventListener("change", function () {
    state.filter = filterEl.value;
    render();
  });

  themeBtn.addEventListener("click", function () {
    var next = document.documentElement.getAttribute("data-theme") === "dark" ? "light" : "dark";
    document.documentElement.setAttribute("data-theme", next);
    try { localStorage.setItem("qqzone-theme", next); } catch (e) {}
  });
  try {
    var saved = localStorage.getItem("qqzone-theme");
    if (saved) document.documentElement.setAttribute("data-theme", saved);
  } catch (e) {}

  feed.addEventListener("click", function (e) {
    var more = e.target.closest("[data-more]");
    if (more) {
      var box = more.previousElementSibling;
      if (box) box.hidden = false;
      more.remove();
      return;
    }
    var img = e.target.closest("img[data-gallery], img.mini");
    if (!img) return;
    var grid = img.closest(".grid");
    var imgs = [];
    if (grid && grid.getAttribute("data-imgs")) {
      try { imgs = JSON.parse(grid.getAttribute("data-imgs")); } catch (err) {}
    }
    if (!imgs.length) imgs = [img.getAttribute("src")];
    var idx = parseInt(img.getAttribute("data-idx") || "0", 10);
    if (img.classList.contains("mini")) {
      imgs = [img.getAttribute("src")];
      idx = 0;
    }
    openLightbox(imgs, idx);
  });

  document.getElementById("lb-close").addEventListener("click", function () { lightbox.hidden = true; });
  document.getElementById("lb-prev").addEventListener("click", function () { moveLightbox(-1); });
  document.getElementById("lb-next").addEventListener("click", function () { moveLightbox(1); });
  lightbox.addEventListener("click", function (e) {
    if (e.target === lightbox) lightbox.hidden = true;
  });
  document.addEventListener("keydown", function (e) {
    if (lightbox.hidden) return;
    if (e.key === "Escape") lightbox.hidden = true;
    if (e.key === "ArrowLeft") moveLightbox(-1);
    if (e.key === "ArrowRight") moveLightbox(1);
  });

  fillHeader();
  renderYears();
  render();
})();

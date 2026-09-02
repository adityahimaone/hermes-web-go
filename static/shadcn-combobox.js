/* shadcn-combobox.js — vanilla shadcn-style searchable Select/Combobox
 * No build, no deps. Requires globals: esc, li, t (optional), _wsStatusDot (optional).
 * Usage:
 *   createWorkspaceCombobox(container, {
 *     workspaces: [{name, path}], value, placeholder,
 *     onSelect(path, item), onCreateNew(), filterFn(term, item) -> bool
 *   })
 * Returns { destroy(), setValue(v), setWorkspaces(list), focus() }
 */
(function () {
  'use strict';

  // ── helpers ───────────────────────────────────────────────────────────────
  function _esc(s) {
    if (typeof esc === 'function') return esc(s);
    return String(s ?? '').replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function _li(name, size) {
    if (typeof li === 'function') return li(name, size);
    return '';
  }
  function _statusDot(path) {
    if (typeof _wsStatusDot === 'function') return _wsStatusDot(path);
    if (!path) return '<span class="ws-status-dot unknown"></span>';
    if (path.startsWith('/home/')) return '<span class="ws-status-dot local" title="Local"></span>';
    if (path.startsWith('/Users/') || path.startsWith('/c/') || path.startsWith('/d/')) return '<span class="ws-status-dot remote" title="Remote"></span>';
    return '<span class="ws-status-dot unknown"></span>';
  }
  function _t(key, fallback) {
    if (typeof t === 'function') { try { var v = t(key); if (v && v !== key) return v; } catch (_) {} }
    return fallback;
  }

  // ── main ────────────────────────────────────────────────────────────────
  // eslint-disable-next-line no-unused-vars
  window.createWorkspaceCombobox = function createWorkspaceCombobox(container, opts) {
    opts = opts || {};
    var workspaces = Array.isArray(opts.workspaces) ? opts.workspaces.slice() : [];
    var value = opts.value || '';
    var placeholder = opts.placeholder || _t('ws_search_placeholder', 'Search workspaces…');
    var onSelect = typeof opts.onSelect === 'function' ? opts.onSelect : function () {};
    var onCreateNew = typeof opts.onCreateNew === 'function' ? opts.onCreateNew : null;
    var filterFn = typeof opts.filterFn === 'function' ? opts.filterFn : null;
    var allowCustomValue = !!opts.allowCustomValue;
    var customPlaceholder = opts.customPlaceholder || placeholder;

    // Alphabetical sort helper
    function _sorted(list) {
      return list.slice().sort(function (a, b) {
        return (a.name || '').localeCompare(b.name || '', undefined, { sensitivity: 'base' });
      });
    }

    // Resolve display label for current value
    function _labelFor(val) {
      if (!val) return '';
      var m = workspaces.find(function (w) { return (w.path || w.value || w) === val; });
      if (m) return m.name || m.path || m.value || val;
      // fallback: last segment
      return String(val).split('/').filter(Boolean).pop() || val;
    }

    // ── DOM ───────────────────────────────────────────────────────────────
    container.innerHTML = '';
    container.classList.add('shadcn-combobox');

    // Trigger button
    var trigger = document.createElement('button');
    trigger.type = 'button';
    trigger.className = 'shadcn-combobox-trigger';
    trigger.setAttribute('aria-haspopup', 'listbox');
    trigger.setAttribute('aria-expanded', 'false');
    trigger.setAttribute('role', 'combobox');

    function _renderTrigger() {
      var label = _labelFor(value);
      if (label) {
        trigger.innerHTML = '<span class="shadcn-combobox-value">' + _esc(label) + '</span>'
          + '<span class="shadcn-combobox-trigger-icon">' + _li('chevrons-up-down', 14) + '</span>';
        trigger.classList.remove('is-placeholder');
        trigger.title = value;
      } else {
        trigger.innerHTML = '<span class="shadcn-combobox-placeholder">' + _esc(customPlaceholder) + '</span>'
          + '<span class="shadcn-combobox-trigger-icon">' + _li('chevrons-up-down', 14) + '</span>';
        trigger.classList.add('is-placeholder');
        trigger.title = '';
      }
    }
    _renderTrigger();

    // Dropdown
    var dropdown = document.createElement('div');
    dropdown.className = 'shadcn-combobox-dropdown';
    dropdown.hidden = true;
    dropdown.setAttribute('role', 'listbox');

    // Search row
    var searchRow = document.createElement('div');
    searchRow.className = 'shadcn-combobox-search-row';
    searchRow.innerHTML = '<span class="shadcn-combobox-search-icon">' + _li('search', 13) + '</span>'
      + '<input class="shadcn-combobox-search-input" type="text" placeholder="' + _esc(placeholder) + '" spellcheck="false" autocomplete="off" aria-autocomplete="list">'
      + '<button class="shadcn-combobox-search-clear" type="button" aria-label="Clear">' + _li('x', 11) + '</button>';
    var searchInput = searchRow.querySelector('.shadcn-combobox-search-input');
    var searchClear = searchRow.querySelector('.shadcn-combobox-search-clear');

    // List container
    var listEl = document.createElement('div');
    listEl.className = 'shadcn-combobox-list';
    listEl.setAttribute('role', 'presentation');

    // Empty state
    var emptyEl = document.createElement('div');
    emptyEl.className = 'shadcn-combobox-empty';
    emptyEl.textContent = _t('ws_no_results', 'No workspaces found');
    emptyEl.style.display = 'none';

    // Create-new action row
    var createRow = null;
    if (onCreateNew) {
      createRow = document.createElement('div');
      createRow.className = 'shadcn-combobox-create-row';
      createRow.setAttribute('role', 'option');
      createRow.tabIndex = -1;
      createRow.innerHTML = '<span class="shadcn-combobox-create-icon">' + _li('plus', 12) + '</span>'
        + '<span class="shadcn-combobox-create-label">' + _esc(_t('workspace_choose_path', 'Create new workspace')) + '</span>';
      createRow.addEventListener('click', function () {
        close();
        onCreateNew();
      });
    }

    dropdown.appendChild(searchRow);
    dropdown.appendChild(listEl);
    dropdown.appendChild(emptyEl);
    if (createRow) {
      var divider = document.createElement('div');
      divider.className = 'shadcn-combobox-divider';
      dropdown.appendChild(divider);
      dropdown.appendChild(createRow);
    }

    container.appendChild(trigger);
    container.appendChild(dropdown);

    // ── State ─────────────────────────────────────────────────────────────
    var open = false;
    var filtered = [];
    var activeIndex = -1; // index into filtered; -1 means none; filtered.length means createRow
    var outsideHandler = null;
    var triggerKeyHandler = null;

    function _matchesFilter(item, term) {
      if (filterFn) return filterFn(term, item);
      if (!term) return true;
      term = term.toLowerCase();
      var name = (item.name || '').toLowerCase();
      var path = (item.path || item.value || '').toLowerCase();
      return name.indexOf(term) !== -1 || path.indexOf(term) !== -1;
    }

    function _buildFiltered(term) {
      term = (term || '').trim();
      var sorted = _sorted(workspaces);
      if (!term) { filtered = sorted; return; }
      filtered = sorted.filter(function (w) { return _matchesFilter(w, term); });
    }

    function _renderList() {
      listEl.innerHTML = '';
      if (!filtered.length) {
        emptyEl.style.display = '';
        listEl.style.display = 'none';
        activeIndex = -1;
        return;
      }
      emptyEl.style.display = 'none';
      listEl.style.display = '';
      filtered.forEach(function (w, idx) {
        var name = w.name || '';
        var path = w.path || w.value || '';
        var isActive = path && path === value;
        var opt = document.createElement('div');
        opt.className = 'shadcn-combobox-option' + (isActive ? ' is-active' : '');
        opt.setAttribute('role', 'option');
        opt.setAttribute('aria-selected', isActive ? 'true' : 'false');
        opt.dataset.index = String(idx);
        // status dot + name + path + ACTIVE badge
        var badge = isActive ? '<span class="shadcn-combobox-badge">ACTIVE</span>' : '';
        var dot = _statusDot(path);
        // Use generic display: if item has both name and path show both, else show whichever exists
        var nameHtml = name ? '<span class="shadcn-combobox-opt-name">' + dot + _esc(name) + badge + '</span>' : '<span class="shadcn-combobox-opt-name">' + dot + _esc(path) + badge + '</span>';
        var pathHtml = (name && path && name !== path) ? '<span class="shadcn-combobox-opt-path">' + _esc(path) + '</span>' : '';
        // check icon for active
        var checkHtml = isActive ? '<span class="shadcn-combobox-check">' + _li('check', 12) + '</span>' : '';
        opt.innerHTML = '<span class="shadcn-combobox-opt-main">' + nameHtml + pathHtml + '</span>' + checkHtml;
        opt.addEventListener('click', function () {
          _selectItem(w);
        });
        opt.addEventListener('mouseenter', function () {
          _setActive(idx);
        });
        listEl.appendChild(opt);
      });
      // reset active to first item or active item
      var activePos = filtered.findIndex(function (w) { return (w.path || w.value || '') === value; });
      if (activePos >= 0) _setActive(activePos);
      else _setActive(0);
    }

    function _setActive(idx) {
      var opts = listEl.querySelectorAll('.shadcn-combobox-option');
      opts.forEach(function (o) { o.classList.remove('is-highlighted'); });
      if (createRow) createRow.classList.remove('is-highlighted');
      activeIndex = idx;
      if (idx >= 0 && idx < filtered.length) {
        if (opts[idx]) {
          opts[idx].classList.add('is-highlighted');
          // scroll into view if needed
          try { opts[idx].scrollIntoView({ block: 'nearest' }); } catch (_) {}
        }
      } else if (idx === filtered.length && createRow) {
        createRow.classList.add('is-highlighted');
        try { createRow.scrollIntoView({ block: 'nearest' }); } catch (_) {}
      }
    }

    function _selectItem(item) {
      var path = item.path || item.value || '';
      value = path;
      _renderTrigger();
      close();
      onSelect(path, item);
    }

    function _selectCustom(val) {
      value = val;
      _renderTrigger();
      close();
      onSelect(val, { path: val, name: val, value: val, _custom: true });
    }

    function openDropdown() {
      if (open) return;
      open = true;
      dropdown.hidden = false;
      trigger.setAttribute('aria-expanded', 'true');
      container.classList.add('is-open');
      // rebuild with current filter
      _buildFiltered(searchInput.value);
      _renderList();
      // focus search input
      requestAnimationFrame(function () { try { searchInput.focus(); } catch (_) {} });
      // outside click
      outsideHandler = function (e) {
        if (!container.contains(e.target)) close();
      };
      // use capture to beat other handlers that might stopPropagation
      setTimeout(function () { document.addEventListener('click', outsideHandler); }, 0);
      // keyboard on dropdown
      dropdown.addEventListener('keydown', _onKeyDown);
    }

    function close() {
      if (!open) return;
      open = false;
      dropdown.hidden = true;
      trigger.setAttribute('aria-expanded', 'false');
      container.classList.remove('is-open');
      if (outsideHandler) { document.removeEventListener('click', outsideHandler); outsideHandler = null; }
      dropdown.removeEventListener('keydown', _onKeyDown);
      // return focus to trigger
      try { trigger.focus(); } catch (_) {}
    }

    function _onKeyDown(e) {
      var maxIdx = filtered.length + (createRow ? 1 : 0) - 1;
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        if (maxIdx < 0) return;
        var next = activeIndex + 1;
        if (next > maxIdx) next = 0;
        _setActive(next);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        if (maxIdx < 0) return;
        var prev = activeIndex - 1;
        if (prev < 0) prev = maxIdx;
        _setActive(prev);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (activeIndex >= 0 && activeIndex < filtered.length) {
          _selectItem(filtered[activeIndex]);
        } else if (activeIndex === filtered.length && createRow) {
          close();
          onCreateNew();
        } else if (allowCustomValue) {
          var customVal = (searchInput.value || '').trim();
          if (customVal) _selectCustom(customVal);
          else close();
        } else {
          // if single filtered result, select it
          if (filtered.length === 1) _selectItem(filtered[0]);
        }
      } else if (e.key === 'Escape') {
        e.preventDefault();
        close();
      } else if (e.key === 'Tab') {
        close();
      }
    }

    // ── Events ────────────────────────────────────────────────────────────
    trigger.addEventListener('click', function (e) {
      e.stopPropagation();
      if (open) close(); else openDropdown();
    });

    triggerKeyHandler = function (e) {
      if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        openDropdown();
      }
    };
    trigger.addEventListener('keydown', triggerKeyHandler);

    searchInput.addEventListener('input', function () {
      _buildFiltered(searchInput.value);
      _renderList();
      // toggle clear button visibility
      searchClear.style.visibility = searchInput.value ? 'visible' : 'hidden';
    });

    searchInput.addEventListener('keydown', _onKeyDown);

    searchClear.style.visibility = 'hidden';
    searchClear.addEventListener('click', function (e) {
      e.stopPropagation();
      searchInput.value = '';
      searchClear.style.visibility = 'hidden';
      _buildFiltered('');
      _renderList();
      searchInput.focus();
    });

    // Allow typing custom value + Enter when allowCustomValue
    // (handled in _onKeyDown Enter branch)

    // Initial build
    _buildFiltered('');
    _renderList();

    // ── Public API ────────────────────────────────────────────────────────
    return {
      destroy: function () {
        close();
        trigger.removeEventListener('keydown', triggerKeyHandler);
        container.innerHTML = '';
        container.classList.remove('shadcn-combobox', 'is-open');
      },
      setValue: function (v) {
        value = v || '';
        _renderTrigger();
        // update active highlight if open
        if (open) _renderList();
      },
      setWorkspaces: function (list) {
        workspaces = Array.isArray(list) ? list.slice() : [];
        _buildFiltered(searchInput ? searchInput.value : '');
        if (open) _renderList();
        else { _buildFiltered(''); _renderList(); }
        _renderTrigger();
      },
      getValue: function () { return value; },
      focus: function () { try { trigger.focus(); } catch (_) {} },
      open: openDropdown,
      close: close,
      get container() { return container; },
      get trigger() { return trigger; },
      get dropdown() { return dropdown; }
    };
  };
})();

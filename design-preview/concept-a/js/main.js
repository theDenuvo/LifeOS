// Concept A: Premium Workspace — Data & Interactions
// Mock data for LifeOS

const TASKS = [
  {id:1, title:"Подготовить презентацию для совета", note:"Слайды 1-15 готовы, нужны 16-20 про риски", priority:"high", deadline:"2026-11-10", project:"Работа", tags:["презентация","совет"], status:"active", created:"2026-11-08", time:"2ч"},
  {id:2, title:"Ответить на письма от клиентов", note:"3 непрочитанных от Acme Corp", priority:"high", deadline:"2026-11-09", project:"Работа", tags:["email","клиенты"], status:"active", created:"2026-11-09", time:"45м"},
  {id:3, title:"Купить продукты на ужин", note:"Сыр, томаты, базилик, спагетти", priority:"medium", deadline:"2026-11-09", project:"Личное", tags:["покупки"], status:"active", created:"2026-11-09", time:"30м"},
  {id:4, title:"Завершить курс AWS (40 часов)", note:"Дедлайн конец квартала", priority:"low", deadline:"2026-12-31", project:"Обучение", tags:["aws","карьера"], status:"active", created:"2026-11-01", time:"40ч"},
  {id:5, title:"Встреча с командой дизайна", note:"Обсудить новую дизайн-систему", priority:"high", deadline:"2026-11-09T14:00", project:"Работа", tags:["встреча","дизайн"], status:"active", created:"2026-11-08", time:"1ч"},
  {id:6, title:"Оплатить счета за интернет и связь", note:"Дедлайн был вчера", priority:"high", deadline:"2026-11-08", project:"Финансы", tags:["счета","просрочено"], status:"overdue", created:"2026-11-01", time:"15м"},
  {id:7, title:"Прочитать статью про RAG архитектуру", note:"Сохранено в Pocket", priority:"low", deadline:null, project:"Обучение", tags:["чтение","rag"], status:"active", created:"2026-11-05", time:"20м"},
  {id:8, title:"Записаться к стоматологу", note:"Профилактика, раз в полгода", priority:"medium", deadline:"2026-11-15", project:"Здоровье", tags:["врач"], status:"active", created:"2026-11-09", time:"10м"},
  {id:9, title:"Написать годовой отчёт", note:"Собрать метрики за Q3-Q4", priority:"high", deadline:"2026-11-20", project:"Работа", tags:["отчёт","квартал"], status:"active", created:"2026-11-09", time:"6ч"},
  {id:10, title:"Изучить Impeccable Skill", note:"Для дизайн-спринта LifeOS", priority:"high", deadline:"2026-11-09", project:"LifeOS", tags:["impeccable","дизайн"], status:"completed", created:"2026-11-09", completedAt:"2026-11-09T10:30", time:"2ч"}
];

const INBOX = [
  {text:"Позвонить маме про воскресенье", time:"09:12"},
  {text:"Идея: добавить в LifeOS режим фокуса с таймером помодоро", time:"09:45"},
  {text:"Книга \"Атомные привычки\" — перечитать главу 3", time:"10:30"},
  {text:"Проверить обновления зависимостей в проекте", time:"11:15"},
  {text:"Забронировать отель для конференции в декабре", time:"12:00"}
];

const ASSISTANT_MESSAGES = [
  {role:"assistant", content:"Доброе утро! На сегодня 5 активных задач, 1 просроченная (счета). Приоритет — презентация к 14:00 и письма клиентов. Хочешь, перенесу стоматолога на следующую неделю?"},
  {role:"user", content:"Да, перенеси стоматолога на среду следующей недели утром"},
  {role:"assistant", content:"Готово. Записал на 12 ноября 09:00. Ещё: заметил, что AWS сертификат висит с 1 ноября без прогресса. Хочешь разбить на этапы по 2 часа в неделю?"}
];

const PROJECTS = ["Работа","Личное","Обучение","Финансы","Здоровье","LifeOS"];

// Utility functions
function formatDate(dateStr) {
  if (!dateStr) return "";
  const d = new Date(dateStr);
  const today = new Date("2026-11-09");
  const diff = Math.floor((d - today) / (1000*60*60*24));
  if (diff === 0) return "Сегодня";
  if (diff === 1) return "Завтра";
  if (diff === -1) return "Вчера";
  if (diff < 0) return `${Math.abs(diff)} дн. назад`;
  if (diff <= 7) return d.toLocaleDateString("ru-RU", {weekday:"short", day:"numeric", month:"short"});
  return d.toLocaleDateString("ru-RU", {day:"numeric", month:"short"});
}

function formatDeadlineTime(dateStr) {
  if (dateStr && dateStr.includes("T")) {
    const t = dateStr.split("T")[1];
    return t.substring(0,5);
  }
  return "";
}

function getPriorityLabel(p) {
  return p === "high" ? "Высокий" : p === "medium" ? "Средний" : "Низкий";
}

function getStatusLabel(s) {
  return s === "active" ? "Активна" : s === "overdue" ? "Просрочена" : s === "completed" ? "Выполнена" : s;
}

function showToast(message, type = "info") {
  const container = document.getElementById("toast-container");
  if (!container) return;
  const toast = document.createElement("div");
  toast.className = `toast toast--${type}`;
  toast.textContent = message;
  container.appendChild(toast);
  setTimeout(() => {
    toast.style.opacity = "0";
    toast.style.transform = "translateY(10px)";
    setTimeout(() => toast.remove(), 300);
  }, 3000);
}

// Screen switching
function switchScreen(screenName) {
  const screens = document.querySelectorAll(".screen");
  screens.forEach(s => s.classList.remove("is-active"));
  const target = document.getElementById(`panel-${screenName}`);
  if (target) {
    target.classList.add("is-active");
    target.removeAttribute("hidden");
  }
  
  const tabs = document.querySelectorAll(".chrome__tab");
  tabs.forEach(t => {
    const isActive = t.dataset.screen === screenName;
    t.classList.toggle("is-active", isActive);
    t.setAttribute("aria-selected", isActive);
  });
  
  const navItems = document.querySelectorAll(".nav-panel__item");
  navItems.forEach(n => {
    n.classList.toggle("is-active", n.dataset.screen === screenName);
  });
  
  const mobileNav = document.querySelectorAll(".mobile-nav__item");
  mobileNav.forEach(n => {
    n.classList.toggle("is-active", n.dataset.screen === screenName);
  });
  
  document.body.dataset.screen = screenName;
  
  // Update stats
  updateStats();
}

// Render Today
function renderToday() {
  const list = document.getElementById("today-list");
  if (!list) return;
  
  const todayTasks = TASKS.filter(t => t.status !== "completed" && (t.deadline === "2026-11-09" || t.deadline === "2026-11-10" || t.deadline === "2026-11-09T14:00" || !t.deadline));
  const overdue = TASKS.filter(t => t.status === "overdue");
  const allActive = [...overdue, ...todayTasks.filter(t => t.status !== "overdue")];
  
  list.innerHTML = allActive.map(task => `
    <li class="task-item ${task.status === 'overdue' ? 'is-overdue' : ''} ${task.priority === 'high' ? 'is-high-priority' : ''}" data-task-id="${task.id}" role="listitem">
      <div class="task-item__checkbox ${task.status === 'completed' ? 'is-checked' : ''}" role="checkbox" aria-checked="${task.status === 'completed'}" tabindex="0" data-task-id="${task.id}" aria-label="Отметить ${task.title} выполненной"></div>
      <div class="task-item__content">
        <h3 class="task-item__title">${task.title}</h3>
        ${task.note ? `<p class="task-item__note">${task.note}</p>` : ''}
        <div class="task-item__meta">
          <span class="task-item__project">${task.project}</span>
          ${task.deadline ? `<span class="task-item__deadline ${task.status === 'overdue' ? 'is-overdue' : (task.deadline === '2026-11-09' || task.deadline === '2026-11-09T14:00' ? 'is-today' : '')}">${formatDate(task.deadline)}${formatDeadlineTime(task.deadline) ? ' ' + formatDeadlineTime(task.deadline) : ''}</span>` : ''}
          <span class="task-item__priority task-item__priority--${task.priority}">${getPriorityLabel(task.priority)}</span>
          ${task.time ? `<span class="task-item__tag">${task.time}</span>` : ''}
          ${task.tags.map(t => `<span class="task-item__tag">#${t}</span>`).join('')}
        </div>
      </div>
      <div class="task-item__actions">
        <button class="task-item__action" aria-label="Редактировать" data-action="edit" data-task-id="${task.id}"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M11 2l3 3-9 9H2v-3L11 2z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
        <button class="task-item__action" aria-label="Удалить" data-action="delete" data-task-id="${task.id}"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M3 4h10M6 4V2h4v2M5 4l1 10h4l1-10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
      </div>
    </li>
  `).join('');
  
  const empty = document.getElementById("today-empty");
  if (empty) {
    empty.hidden = allActive.length > 0;
    list.hidden = allActive.length === 0;
  }
}

// Render Inbox
function renderInbox() {
  const list = document.getElementById("inbox-list");
  if (!list) return;
  
  list.innerHTML = INBOX.map((item, i) => `
    <div class="inbox-item" role="listitem" data-inbox-index="${i}">
      <div class="task-item__checkbox" role="checkbox" aria-checked="false" tabindex="0" data-inbox-index="${i}" aria-label="Отметить обработанным"></div>
      <div class="inbox-item__content">
        <p class="inbox-item__text">${item.text}</p>
        <span class="inbox-item__time">${item.time}</span>
      </div>
      <div class="inbox-item__actions">
        <button class="btn btn--ghost btn--icon" aria-label="Превратить в задачу" data-action="inbox-to-task" data-inbox-index="${i}"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M8 2v12M2 8h12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg></button>
        <button class="btn btn--ghost btn--icon" aria-label="Отклонить" data-action="inbox-dismiss" data-inbox-index="${i}"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg></button>
      </div>
    </div>
  `).join('');
  
  const empty = document.getElementById("inbox-empty");
  if (empty) {
    empty.hidden = INBOX.length > 0;
    list.hidden = INBOX.length === 0;
  }
}

// Render All Tasks
function renderAllTasks() {
  const tbody = document.getElementById("tasks-table-body");
  if (!tbody) return;
  
  const projectFilter = document.getElementById("filter-project")?.value || "";
  const statusFilter = document.getElementById("filter-status")?.value || "";
  const priorityFilter = document.getElementById("filter-priority")?.value || "";
  
  let filtered = TASKS.filter(t => {
    if (projectFilter && t.project !== projectFilter) return false;
    if (statusFilter && t.status !== statusFilter) return false;
    if (priorityFilter && t.priority !== priorityFilter) return false;
    return true;
  });
  
  tbody.innerHTML = filtered.map(task => `
    <tr role="row">
      <td><div class="table__checkbox ${task.status === 'completed' ? 'is-checked' : ''}" role="checkbox" aria-checked="${task.status === 'completed'}" tabindex="0" data-task-id="${task.id}"></div></td>
      <td><span class="tasks-table__title">${task.title}</span></td>
      <td><span class="tasks-table__project">${task.project}</span></td>
      <td><span class="task-item__priority task-item__priority--${task.priority}">${getPriorityLabel(task.priority)}</span></td>
      <td><span class="tasks-table__deadline ${task.status === 'overdue' ? 'is-overdue' : ''}">${task.deadline ? formatDate(task.deadline) + (formatDeadlineTime(task.deadline) ? ' ' + formatDeadlineTime(task.deadline) : '') : '—'}</span></td>
      <td><span class="tasks-table__status tasks-table__status--${task.status}">${getStatusLabel(task.status)}</span></td>
      <td>
        <button class="task-item__action" aria-label="Действия" data-task-id="${task.id}"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="3" r="1.5" fill="currentColor"/><circle cx="8" cy="8" r="1.5" fill="currentColor"/><circle cx="8" cy="13" r="1.5" fill="currentColor"/></svg></button>
      </td>
    </tr>
  `).join('');
  
  // Update project filter
  const projectSelect = document.getElementById("filter-project");
  if (projectSelect && projectSelect.options.length <= 1) {
    PROJECTS.forEach(p => {
      const opt = document.createElement("option");
      opt.value = p;
      opt.textContent = p;
      projectSelect.appendChild(opt);
    });
  }
  
  // Render board view
  renderBoard();
  // Render list view
  renderListView();
}

function renderBoard() {
  const board = document.getElementById("tasks-board-view");
  if (!board) return;
  
  const columns = [
    {key:"active", label:"Активные", tasks: TASKS.filter(t => t.status === "active")},
    {key:"overdue", label:"Просроченные", tasks: TASKS.filter(t => t.status === "overdue")},
    {key:"completed", label:"Выполненные", tasks: TASKS.filter(t => t.status === "completed")}
  ];
  
  board.innerHTML = columns.map(col => `
    <div class="board-column" role="list" aria-label="${col.label}">
      <div class="board-column__header">
        <span>${col.label}</span>
        <span class="board-column__count">${col.tasks.length}</span>
      </div>
      ${col.tasks.map(task => `
        <div class="board-card" role="listitem" draggable="true" data-task-id="${task.id}">
          <h3 class="board-card__title">${task.title}</h3>
          <div class="board-card__meta">
            <span class="priority-dot priority-dot--${task.priority}"></span>
            <span>${task.project}</span>
            ${task.deadline ? `<span>· ${formatDate(task.deadline)}</span>` : ''}
          </div>
        </div>
      `).join('')}
    </div>
  `).join('');
}

function renderListView() {
  const list = document.getElementById("tasks-list-view");
  if (!list) return;
  
  list.innerHTML = TASKS.map(task => `
    <li class="task-item ${task.status === 'overdue' ? 'is-overdue' : ''} ${task.priority === 'high' ? 'is-high-priority' : ''}" data-task-id="${task.id}" role="listitem">
      <div class="task-item__checkbox ${task.status === 'completed' ? 'is-checked' : ''}" role="checkbox" aria-checked="${task.status === 'completed'}" tabindex="0" data-task-id="${task.id}"></div>
      <div class="task-item__content">
        <h3 class="task-item__title">${task.title}</h3>
        <div class="task-item__meta">
          <span class="task-item__project">${task.project}</span>
          ${task.deadline ? `<span class="task-item__deadline ${task.status === 'overdue' ? 'is-overdue' : ''}">${formatDate(task.deadline)}</span>` : ''}
        </div>
      </div>
    </li>
  `).join('');
}

// Render Assistant
function renderAssistant() {
  const container = document.getElementById("assistant-messages");
  if (!container) return;
  
  container.innerHTML = ASSISTANT_MESSAGES.map(msg => `
    <div class="assistant__message assistant__message--${msg.role}">
      <div class="assistant__avatar">${msg.role === 'assistant' ? '🤖' : '👤'}</div>
      <div class="assistant__bubble">
        ${msg.content.split('\n').map(p => `<p>${p}</p>`).join('')}
      </div>
    </div>
  `).join('');
  
  // Scroll to bottom
  container.scrollTop = container.scrollHeight;
}

// Update Stats
function updateStats() {
  const active = TASKS.filter(t => t.status === "active").length;
  const overdue = TASKS.filter(t => t.status === "overdue").length;
  const high = TASKS.filter(t => t.priority === "high" && t.status !== "completed").length;
  const done = TASKS.filter(t => t.status === "completed").length;
  
  const statActive = document.getElementById("stat-active");
  const statOverdue = document.getElementById("stat-overdue");
  const statHigh = document.getElementById("stat-high");
  const statDone = document.getElementById("stat-done");
  
  if (statActive) statActive.textContent = active;
  if (statOverdue) statOverdue.textContent = overdue;
  if (statHigh) statHigh.textContent = high;
  if (statDone) statDone.textContent = done;
}

// Event Handlers
function initEventHandlers() {
  // Screen switching
  document.querySelectorAll(".chrome__tab, .nav-panel__item, .mobile-nav__item").forEach(el => {
    el.addEventListener("click", () => {
      const screen = el.dataset.screen;
      if (screen) switchScreen(screen);
      // Close nav panel on mobile
      const navPanel = document.getElementById("nav-panel");
      if (navPanel) navPanel.classList.remove("is-open");
    });
  });
  
  // Chrome menu toggle
  const menuBtn = document.querySelector(".chrome__menu");
  const navPanel = document.getElementById("nav-panel");
  if (menuBtn && navPanel) {
    menuBtn.addEventListener("click", () => {
      navPanel.classList.toggle("is-open");
      menuBtn.setAttribute("aria-expanded", navPanel.classList.contains("is-open"));
    });
    navPanel.querySelector(".nav-panel__backdrop")?.addEventListener("click", () => {
      navPanel.classList.remove("is-open");
      menuBtn.setAttribute("aria-expanded", "false");
    });
  }
  
  // Quick add
  const quickAddBtn = document.querySelector(".chrome__quick-add");
  const quickAddPanel = document.getElementById("quick-add-panel");
  if (quickAddBtn && quickAddPanel) {
    quickAddBtn.addEventListener("click", () => {
      quickAddPanel.classList.add("is-open");
      quickAddBtn.setAttribute("aria-expanded", "true");
    });
    quickAddPanel.querySelector(".quick-add__backdrop")?.addEventListener("click", closeQuickAdd);
    quickAddPanel.querySelector(".quick-add__cancel")?.addEventListener("click", closeQuickAdd);
    quickAddPanel.querySelector(".quick-add__close")?.addEventListener("click", closeQuickAdd);
  }
  
  function closeQuickAdd() {
    quickAddPanel.classList.remove("is-open");
    quickAddBtn.setAttribute("aria-expanded", "false");
  }
  
  // Quick add form submit
  const quickAddForm = document.getElementById("quick-add-form");
  if (quickAddForm) {
    quickAddForm.addEventListener("submit", (e) => {
      e.preventDefault();
      const title = document.getElementById("qa-title").value.trim();
      if (!title) return;
      const newTask = {
        id: TASKS.length + 1,
        title: title,
        note: document.getElementById("qa-note").value.trim(),
        priority: document.getElementById("qa-priority").value,
        deadline: document.getElementById("qa-deadline").value || null,
        project: document.getElementById("qa-project").value,
        tags: document.getElementById("qa-tags").value.split(",").map(t => t.trim()).filter(Boolean),
        status: "active",
        created: new Date().toISOString().split("T")[0],
        time: "15м"
      };
      TASKS.push(newTask);
      renderToday();
      renderAllTasks();
      updateStats();
      closeQuickAdd();
      quickAddForm.reset();
      showToast("Задача создана", "success");
    });
  }
  
  // Command palette
  const cmdPalette = document.getElementById("command-palette");
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      cmdPalette.classList.toggle("is-open");
    }
    if (e.key === "Escape") {
      cmdPalette.classList.remove("is-open");
      closeQuickAdd();
      const navPanel = document.getElementById("nav-panel");
      if (navPanel) navPanel.classList.remove("is-open");
      const focusOverlay = document.getElementById("focus-overlay");
      if (focusOverlay) focusOverlay.classList.remove("is-open");
    }
    if ((e.metaKey || e.ctrlKey) && e.key === "n") {
      e.preventDefault();
      if (quickAddPanel) {
        quickAddPanel.classList.add("is-open");
        if (quickAddBtn) quickAddBtn.setAttribute("aria-expanded", "true");
      }
    }
  });
  
  if (cmdPalette) {
    cmdPalette.querySelector(".command-palette__backdrop")?.addEventListener("click", () => {
      cmdPalette.classList.remove("is-open");
    });
  }
  
  // Task checkbox toggle
  document.addEventListener("click", (e) => {
    const checkbox = e.target.closest(".task-item__checkbox, .table__checkbox");
    if (checkbox) {
      const taskId = parseInt(checkbox.dataset.taskId);
      const task = TASKS.find(t => t.id === taskId);
      if (task) {
        task.status = task.status === "completed" ? "active" : "completed";
        if (task.status === "completed") {
          task.completedAt = new Date().toISOString();
        }
        renderToday();
        renderAllTasks();
        updateStats();
        showToast(task.status === "completed" ? "Задача выполнена" : "Задача восстановлена", "success");
      }
    }
  });
  
  // Filter changes
  ["filter-project", "filter-status", "filter-priority"].forEach(id => {
    const el = document.getElementById(id);
    if (el) el.addEventListener("change", renderAllTasks);
  });
  
  // View switching
  document.querySelectorAll(".toolbar__btn[data-view]").forEach(btn => {
    btn.addEventListener("click", () => {
      const view = btn.dataset.view;
      document.querySelectorAll(".toolbar__btn[data-view]").forEach(b => {
        b.classList.remove("is-active");
        b.setAttribute("aria-pressed", "false");
      });
      btn.classList.add("is-active");
      btn.setAttribute("aria-pressed", "true");
      
      document.getElementById("tasks-table-view").hidden = view !== "table";
      document.getElementById("tasks-board-view").hidden = view !== "board";
      document.getElementById("tasks-list-view").hidden = view !== "list";
    });
  });
  
  // Focus mode
  document.querySelectorAll('[data-action="focus-mode"]').forEach(btn => {
    btn.addEventListener("click", () => {
      const activeTask = TASKS.find(t => t.status === "active");
      if (activeTask) {
        const overlay = document.getElementById("focus-overlay");
        document.getElementById("focus-task-title").textContent = activeTask.title;
        document.getElementById("focus-task-content").innerHTML = `
          <p><strong>Проект:</strong> ${activeTask.project}</p>
          <p><strong>Приоритет:</strong> ${getPriorityLabel(activeTask.priority)}</p>
          <p><strong>Дедлайн:</strong> ${activeTask.deadline ? formatDate(activeTask.deadline) : 'Нет'}</p>
          <p><strong>Оценка времени:</strong> ${activeTask.time}</p>
          <p><strong>Заметка:</strong> ${activeTask.note || '—'}</p>
        `;
        overlay.classList.add("is-open");
      }
    });
  });
  
  const focusOverlay = document.getElementById("focus-overlay");
  if (focusOverlay) {
    focusOverlay.querySelector(".focus-overlay__backdrop")?.addEventListener("click", () => {
      focusOverlay.classList.remove("is-open");
    });
    focusOverlay.querySelector(".focus-overlay__close")?.addEventListener("click", () => {
      focusOverlay.classList.remove("is-open");
    });
  }
  
  // Assistant composer
  const assistantInput = document.getElementById("assistant-input");
  const assistantComposer = document.getElementById("assistant-composer");
  if (assistantInput) {
    assistantInput.addEventListener("input", function() {
      this.style.height = "auto";
      this.style.height = Math.min(this.scrollHeight, 120) + "px";
    });
  }
  
  if (assistantComposer) {
    assistantComposer.addEventListener("submit", (e) => {
      e.preventDefault();
      const input = document.getElementById("assistant-input");
      const text = input.value.trim();
      if (!text) return;
      
      ASSISTANT_MESSAGES.push({role:"user", content: text});
      
      // Simulate assistant response
      setTimeout(() => {
        ASSISTANT_MESSAGES.push({
          role:"assistant",
          content: `Принято: «${text}». Обрабатываю…\n\nСоздал задачу «${text}» с приоритетом средний. Назначил на сегодня. Хотите изменить дедлайн или приоритет?`
        });
        renderAssistant();
      }, 800);
      
      input.value = "";
      input.style.height = "auto";
      renderAssistant();
    });
  }
  
  // Assistant hints
  document.querySelectorAll(".assistant__hint").forEach(hint => {
    hint.addEventListener("click", () => {
      const input = document.getElementById("assistant-input");
      if (input) {
        input.value = hint.dataset.insert + " ";
        input.focus();
      }
    });
  });
  
  // Empty state actions
  document.querySelectorAll('[data-action="quick-add"]').forEach(btn => {
    btn.addEventListener("click", () => {
      const quickAddPanel = document.getElementById("quick-add-panel");
      const quickAddBtn = document.querySelector(".chrome__quick-add");
      if (quickAddPanel) {
        quickAddPanel.classList.add("is-open");
        if (quickAddBtn) quickAddBtn.setAttribute("aria-expanded", "true");
      }
    });
  });
  
  // Toast for data-action buttons
  document.querySelectorAll('[data-action="inbox-process-all"]').forEach(btn => {
    btn.addEventListener("click", () => {
      showToast("Все элементы обработаны", "success");
    });
  });
}

// Initialize
function init() {
  renderToday();
  renderInbox();
  renderAllTasks();
  renderAssistant();
  updateStats();
  initEventHandlers();
  
  // Mobile nav injection
  if (window.innerWidth <= 768) {
    const mobileNav = document.createElement("nav");
    mobileNav.className = "mobile-nav";
    mobileNav.setAttribute("aria-label", "Мобильная навигация");
    mobileNav.innerHTML = `
      <button class="mobile-nav__item is-active" data-screen="today">
        <svg viewBox="0 0 20 20" fill="none"><path d="M4 5h12v10H4zM8 9h8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
        <span>Сегодня</span>
      </button>
      <button class="mobile-nav__item" data-screen="inbox">
        <svg viewBox="0 0 20 20" fill="none"><path d="M14 5H6a2 2 0 00-2 2v10a2 2 0 002 2h8a2 2 0 002-2V10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><path d="M10 14h4M10 10h4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
        <span>Входящие</span>
      </button>
      <button class="mobile-nav__item" data-screen="all">
        <svg viewBox="0 0 20 20" fill="none"><rect x="3" y="4" width="14" height="12" rx="1.5" stroke="currentColor" stroke-width="1.5"/><path d="M3 9h14M8 4v12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
        <span>Все</span>
      </button>
      <button class="mobile-nav__item" data-screen="assistant">
        <svg viewBox="0 0 20 20" fill="none"><path d="M10 3a7 7 0 100 14A7 7 0 0010 3zm0 1a6 6 0 110 12 6 6 0 010-12z" stroke="currentColor" stroke-width="1.5"/><path d="M10 7v5M10 12v.01" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
        <span>Ассистент</span>
      </button>
    `;
    document.querySelector(".app").appendChild(mobileNav);
  }
}

document.addEventListener("DOMContentLoaded", init);

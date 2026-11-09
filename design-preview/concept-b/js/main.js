// Concept B: Intelligent Command Center — Data & Interactions
// Refined Phase 1

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
  {text:'Книга "Атомные привычки" — перечитать главу 3', time:"10:30"},
  {text:"Проверить обновления зависимостей в проекте", time:"11:15"},
  {text:"Забронировать отель для конференции в декабре", time:"12:00"}
];

const ASSISTANT_MESSAGES = [
  {role:"assistant", content:"Доброе утро! На сегодня 5 активных задач, 1 просроченная (счета). Приоритет — презентация к 14:00 и письма клиентов. Хочешь, перенесу стоматолога на следующую неделю?"},
  {role:"user", content:"Да, перенеси стоматолога на среду следующей недели утром"},
  {role:"assistant", content:"Готово. Записал на 12 ноября 09:00. Ещё: заметил, что AWS сертификат висит с 1 ноября без прогресса. Хочешь разбить на этапы по 2 часа в неделю?"}
];

const DEMO_DATE = "2026-11-09";

function formatDate(dateStr) {
  if (!dateStr) return "—";
  const d = new Date(dateStr);
  const today = new Date(DEMO_DATE);
  const diff = Math.floor((d - today) / (1000 * 60 * 60 * 24));
  if (diff === 0) return "Сегодня";
  if (diff === 1) return "Завтра";
  if (diff === -1) return "Вчера";
  if (diff < 0) return `${Math.abs(diff)} дн. назад`;
  if (diff <= 7) return d.toLocaleDateString("ru-RU", { weekday: "short", day: "numeric", month: "short" });
  return d.toLocaleDateString("ru-RU", { day: "numeric", month: "short" });
}

function getPriorityLabel(p) {
  return p === "high" ? "Высокий" : p === "medium" ? "Средний" : "Низкий";
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

function switchScreen(screenName) {
  document.querySelectorAll(".screen").forEach(s => s.classList.remove("is-active"));
  const target = document.getElementById(`panel-${screenName}`);
  if (target) { target.classList.add("is-active"); target.removeAttribute("hidden"); }

  document.querySelectorAll(".sidebar__item").forEach(t => {
    t.classList.toggle("is-active", t.dataset.screen === screenName);
    t.setAttribute("aria-current", t.dataset.screen === screenName ? "page" : "false");
  });

  document.querySelectorAll(".mobile-nav__item").forEach(n => {
    n.classList.toggle("is-active", n.dataset.screen === screenName);
  });

  document.body.dataset.screen = screenName;
  updateAssistantContext(screenName);
}

function createElement(tag, className, textContent) {
  const el = document.createElement(tag);
  if (className) el.className = className;
  if (textContent !== undefined && textContent !== null) el.textContent = String(textContent);
  return el;
}

function createTaskCard(task) {
  const card = document.createElement("div");
  card.className = `task-card ${task.status === 'overdue' ? 'is-overdue' : ''} ${task.status === 'completed' ? 'is-completed' : ''}`;
  card.dataset.taskId = task.id;

  const header = document.createElement("div");
  header.className = "task-card__header";

  const title = document.createElement("h3");
  title.className = "task-card__title";
  title.textContent = task.title;
  header.appendChild(title);

  const checkbox = document.createElement("div");
  checkbox.className = `task-card__checkbox ${task.status === 'completed' ? 'is-checked' : ''}`;
  checkbox.dataset.taskId = task.id;
  checkbox.setAttribute("role", "checkbox");
  checkbox.setAttribute("aria-checked", task.status === "completed");
  checkbox.setAttribute("tabindex", "0");
  header.appendChild(checkbox);

  card.appendChild(header);

  if (task.note) {
    const note = document.createElement("p");
    note.className = "task-card__note";
    note.textContent = task.note;
    card.appendChild(note);
  }

  const meta = document.createElement("div");
  meta.className = "task-card__meta";

  const project = document.createElement("span");
  project.className = "task-card__project";
  project.textContent = task.project;
  meta.appendChild(project);

  if (task.deadline) {
    const deadline = document.createElement("span");
    let deadlineClass = "task-card__deadline";
    if (task.status === 'overdue') deadlineClass += " is-overdue";
    else if (task.deadline === DEMO_DATE || task.deadline === DEMO_DATE + "T14:00") deadlineClass += " is-today";
    deadline.className = deadlineClass;
    deadline.textContent = formatDate(task.deadline);
    meta.appendChild(deadline);
  }

  card.appendChild(meta);

  if (task.tags && task.tags.length) {
    const tags = document.createElement("div");
    tags.className = "task-card__tags";
    task.tags.forEach(t => {
      const tag = document.createElement("span");
      tag.className = "task-card__tag";
      tag.textContent = `#${t}`;
      tags.appendChild(tag);
    });
    card.appendChild(tags);
  }

  return card;
}

function createTimelineItem(task) {
  const item = document.createElement("div");
  item.className = "timeline__item";

  const time = document.createElement("span");
  time.className = "timeline__time";
  time.textContent = task.deadline && task.deadline.includes("T") ? task.deadline.split("T")[1].substring(0, 5) : "—";
  item.appendChild(time);

  const dot = document.createElement("span");
  dot.className = `timeline__dot timeline__dot--${task.priority}`;
  item.appendChild(dot);

  const label = document.createElement("span");
  label.className = "timeline__label";
  label.textContent = task.title;
  item.appendChild(label);

  return item;
}

function createInboxCard(item, index) {
  const card = document.createElement("div");
  card.className = "inbox-card";
  card.dataset.inboxIndex = index;

  const checkbox = document.createElement("div");
  checkbox.className = "inbox-card__checkbox";
  checkbox.dataset.inboxIndex = index;
  checkbox.setAttribute("role", "checkbox");
  checkbox.setAttribute("aria-checked", "false");
  checkbox.setAttribute("tabindex", "0");
  card.appendChild(checkbox);

  const content = document.createElement("div");
  content.className = "inbox-card__content";

  const text = document.createElement("p");
  text.className = "inbox-card__text";
  text.textContent = item.text;
  content.appendChild(text);

  const meta = document.createElement("div");
  meta.className = "inbox-card__meta";

  const timeSpan = document.createElement("span");
  timeSpan.textContent = item.time;
  meta.appendChild(timeSpan);

  const separator = document.createElement("span");
  separator.textContent = "· быстрый ввод";
  meta.appendChild(separator);

  content.appendChild(meta);
  card.appendChild(content);

  const actions = document.createElement("div");
  actions.className = "inbox-card__actions";

  const toTaskBtn = document.createElement("button");
  toTaskBtn.className = "btn btn--ghost btn--icon";
  toTaskBtn.setAttribute("aria-label", "В задачу");
  toTaskBtn.dataset.action = "inbox-to-task";
  toTaskBtn.dataset.inboxIndex = index;
  toTaskBtn.innerHTML = '<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M8 2v12M2 8h12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>';
  actions.appendChild(toTaskBtn);

  const dismissBtn = document.createElement("button");
  dismissBtn.className = "btn btn--ghost btn--icon";
  dismissBtn.setAttribute("aria-label", "Удалить");
  dismissBtn.dataset.action = "inbox-dismiss";
  dismissBtn.dataset.inboxIndex = index;
  dismissBtn.innerHTML = '<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>';
  actions.appendChild(dismissBtn);

  card.appendChild(actions);
  return card;
}

function createTaskRow(task) {
  const row = document.createElement("div");
  row.className = `task-row ${task.status === 'overdue' ? 'is-overdue' : ''} ${task.status === 'completed' ? 'is-completed' : ''}`;
  row.dataset.taskId = task.id;

  const title = document.createElement("h3");
  title.className = "task-row__title";
  title.textContent = task.title;
  row.appendChild(title);

  const meta = document.createElement("div");
  meta.className = "task-row__meta";

  const priorityDot = document.createElement("span");
  priorityDot.className = `priority-dot priority-dot--${task.priority}`;
  meta.appendChild(priorityDot);

  const project = document.createElement("span");
  project.textContent = task.project;
  meta.appendChild(project);

  if (task.deadline) {
    const deadline = document.createElement("span");
    deadline.textContent = `· ${formatDate(task.deadline)}`;
    meta.appendChild(deadline);
  }

  const status = document.createElement("span");
  status.className = `task-row__status task-row__status--${task.status}`;
  status.textContent = task.status === 'active' ? 'Активна' : task.status === 'overdue' ? 'Просрочена' : 'Выполнена';
  meta.appendChild(status);

  row.appendChild(meta);
  return row;
}

function createAssistantMessage(msg) {
  const message = document.createElement("div");
  message.className = `assistant__message assistant__message--${msg.role}`;

  const avatar = document.createElement("div");
  avatar.className = "assistant__avatar";
  avatar.textContent = msg.role === 'assistant' ? '🤖' : '👤';
  message.appendChild(avatar);

  const bubble = document.createElement("div");
  bubble.className = "assistant__bubble";
  msg.content.split('\n').forEach(p => {
    const para = document.createElement("p");
    para.textContent = p;
    bubble.appendChild(para);
  });
  message.appendChild(bubble);
  return message;
}

function renderToday() {
  const timeline = document.getElementById("today-timeline");
  if (timeline) {
    timeline.textContent = "";
    const todayTasks = TASKS.filter(t => t.status !== "completed").slice(0, 6);
    todayTasks.forEach(task => timeline.appendChild(createTimelineItem(task)));
  }

  const grid = document.getElementById("today-grid");
  if (grid) {
    grid.textContent = "";
    const activeTasks = TASKS.filter(t => t.status !== "completed");
    activeTasks.forEach(task => grid.appendChild(createTaskCard(task)));
  }

  const focusTask = TASKS.find(t => t.priority === "high" && t.status === "active");
  if (focusTask) {
    const focusTitle = document.getElementById("focus-title");
    const focusMeta = document.getElementById("focus-meta");
    if (focusTitle) focusTitle.textContent = focusTask.title;
    if (focusMeta) focusMeta.textContent = `${focusTask.project} · ${getPriorityLabel(focusTask.priority)} · ${focusTask.time}`;
  }

  updateCounts();
  updateTodaySubtitle();
}

function renderInbox() {
  const stream = document.getElementById("inbox-stream");
  if (!stream) return;
  stream.textContent = "";
  INBOX.forEach((item, i) => stream.appendChild(createInboxCard(item, i)));
}

function renderAllTasks() {
  const view = document.getElementById("tasks-view");
  if (!view) return;

  const statusFilter = document.getElementById("filter-status")?.value || "";
  const priorityFilter = document.getElementById("filter-priority")?.value || "";

  let filtered = TASKS.filter(t => {
    if (statusFilter && t.status !== statusFilter) return false;
    if (priorityFilter && t.priority !== priorityFilter) return false;
    return true;
  });

  view.textContent = "";
  const grid = document.createElement("div");
  grid.className = "tasks-view__grid";
  filtered.forEach(task => grid.appendChild(createTaskRow(task)));
  view.appendChild(grid);

  updateAllTasksSubtitle(filtered.length);
}

function renderAssistant() {
  const container = document.getElementById("assistant-messages");
  if (!container) return;
  container.textContent = "";
  ASSISTANT_MESSAGES.forEach(msg => container.appendChild(createAssistantMessage(msg)));
  container.scrollTop = container.scrollHeight;
}

function updateCounts() {
  const activeCount = TASKS.filter(t => t.status !== "completed").length;
  const overdueCount = TASKS.filter(t => t.status === "overdue").length;
  const inboxCount = INBOX.length;

  const countToday = document.getElementById("count-today");
  const countInbox = document.getElementById("count-inbox");
  const countAll = document.getElementById("count-all");

  if (countToday) countToday.textContent = activeCount;
  if (countInbox) countInbox.textContent = inboxCount;
  if (countAll) countAll.textContent = TASKS.length;

  const statOverdue = document.getElementById("stat-overdue");
  const statHigh = document.getElementById("stat-high");
  if (statOverdue) statOverdue.textContent = overdueCount;
  if (statHigh) statHigh.textContent = TASKS.filter(t => t.priority === "high" && t.status !== "completed").length;
}

function updateTodaySubtitle() {
  const subtitle = document.getElementById("today-subtitle");
  if (subtitle) {
    const activeCount = TASKS.filter(t => t.status !== "completed").length;
    const overdueCount = TASKS.filter(t => t.status === "overdue").length;
    const weekday = new Date(DEMO_DATE).toLocaleDateString("ru-RU", { weekday: "long" });
    const dateStr = new Date(DEMO_DATE).toLocaleDateString("ru-RU", { day: "numeric", month: "long" });
    subtitle.textContent = `${weekday.charAt(0).toUpperCase() + weekday.slice(1)}, ${dateStr} · ${activeCount} активных задач${overdueCount > 0 ? `, ${overdueCount} просроченных` : ''}`;
  }
}

function updateAllTasksSubtitle(count) {
  const subtitle = document.getElementById("all-subtitle");
  if (subtitle) {
    subtitle.textContent = `${count} задач · ${TASKS.length} всего`;
  }
}

function updateAssistantContext(screenName) {
  const screenLabels = { today: "Сегодня", inbox: "Входящие", all: "Все задачи", assistant: "Ассистент" };
  const ctxScreen = document.getElementById("ctx-screen");
  const ctxActive = document.getElementById("ctx-active");
  const ctxOverdue = document.getElementById("ctx-overdue");
  const ctxTime = document.getElementById("ctx-time");

  if (ctxScreen) ctxScreen.textContent = screenLabels[screenName] || screenName;
  if (ctxActive) ctxActive.textContent = TASKS.filter(t => t.status !== "completed").length;
  if (ctxOverdue) ctxOverdue.textContent = TASKS.filter(t => t.status === "overdue").length;
  if (ctxTime) ctxTime.textContent = new Date().toLocaleTimeString("ru-RU", { timeZone: "Europe/Moscow", hour: "2-digit", minute: "2-digit" });
}

function showOperation(text) {
  const op = document.getElementById("assistant-operation");
  const opText = op?.querySelector(".assistant__operation-text");
  if (op) { op.hidden = false; if (opText) opText.textContent = text; }
}

function hideOperation() {
  const op = document.getElementById("assistant-operation");
  if (op) op.hidden = true;
}

function showError(message) {
  const err = document.getElementById("assistant-error");
  const errText = err?.querySelector(".assistant__error-text");
  if (err) { err.hidden = false; if (errText) errText.textContent = message; }
}

function hideError() {
  const err = document.getElementById("assistant-error");
  if (err) err.hidden = true;
}

function applyQuickFilter(filter) {
  document.querySelectorAll(".sidebar__filter").forEach(btn => {
    btn.classList.toggle("is-active", btn.dataset.filter === filter);
  });

  const statusSelect = document.getElementById("filter-status");
  const prioritySelect = document.getElementById("filter-priority");
  if (statusSelect) statusSelect.value = "";
  if (prioritySelect) prioritySelect.value = "";

  switchScreen("all");

  const filterLabels = { today: "Сегодня", overdue: "Просроченные", high: "Высокий приоритет", "no-deadline": "Без срока" };
  showToast(`Фильтр: ${filterLabels[filter]}`, "info");
}

function initKeyboardNav() {
  document.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === " ") {
      const cb = e.target.closest(".task-card__checkbox, .inbox-card__checkbox");
      if (cb) { e.preventDefault(); cb.click(); return; }
    }
    if (e.key === "Escape") hideError();
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      const cmdInput = document.querySelector(".topbar__command-input");
      if (cmdInput) cmdInput.focus();
    }
  });
}

function initEventHandlers() {
  document.querySelectorAll(".sidebar__item, .mobile-nav__item").forEach(el => {
    el.addEventListener("click", () => {
      const screen = el.dataset.screen;
      if (screen) switchScreen(screen);
    });
  });

  document.querySelectorAll(".sidebar__filter").forEach(btn => {
    btn.addEventListener("click", () => applyQuickFilter(btn.dataset.filter));
  });

  document.addEventListener("click", (e) => {
    const cb = e.target.closest(".task-card__checkbox");
    if (cb) {
      const taskId = parseInt(cb.dataset.taskId);
      const task = TASKS.find(t => t.id === taskId);
      if (task) {
        task.status = task.status === "completed" ? "active" : "completed";
        renderToday();
        renderAllTasks();
        showToast(task.status === "completed" ? "Готово!" : "Восстановлено", "success");
      }
    }
  });

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

      ASSISTANT_MESSAGES.push({role: "user", content: text});
      renderAssistant();

      showOperation("Обрабатываю запрос…");
      hideError();

      const sendBtn = document.getElementById("assistant-send");
      if (sendBtn) sendBtn.disabled = true;

      input.value = "";
      input.style.height = "auto";

      setTimeout(() => {
        const demoResponse = `Понял: «${text}». Обрабатываю…\n\nДемо-режим: задача «${text}» (приоритет средний, дедлайн сегодня). В реальном приложении задача будет создана в базе данных.\n\nИзменить приоритет или дедлайн?`;
        ASSISTANT_MESSAGES.push({role: "assistant", content: demoResponse});
        renderAssistant();
        hideOperation();
        if (sendBtn) sendBtn.disabled = false;
      }, 1200);
    });
  }

  const retryBtn = document.getElementById("assistant-retry");
  if (retryBtn) {
    retryBtn.addEventListener("click", () => {
      hideError();
      showToast("Повторный запрос…", "info");
    });
  }

  document.querySelectorAll(".quick-prompt").forEach(btn => {
    btn.addEventListener("click", () => {
      const input = document.getElementById("assistant-input");
      if (input) { input.value = btn.dataset.prompt; input.focus(); }
    });
  });

  ["filter-status", "filter-priority"].forEach(id => {
    const el = document.getElementById(id);
    if (el) el.addEventListener("change", renderAllTasks);
  });

  document.addEventListener("click", (e) => {
    const btn = e.target.closest('[data-action="inbox-to-task"]');
    if (btn) {
      const index = parseInt(btn.dataset.inboxIndex);
      const item = INBOX[index];
      if (item) showToast(`Задача создана: «${item.text}» (демо)`, "success");
    }
    const dismiss = e.target.closest('[data-action="inbox-dismiss"]');
    if (dismiss) {
      const index = parseInt(dismiss.dataset.inboxIndex);
      INBOX.splice(index, 1);
      renderInbox();
      updateCounts();
      showToast("Удалено", "info");
    }
  });

  const captureInput = document.querySelector(".capture-bar__input");
  if (captureInput) {
    captureInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && captureInput.value.trim()) {
        const text = captureInput.value.trim();
        INBOX.push({ text, time: new Date().toLocaleTimeString("ru-RU", { timeZone: "Europe/Moscow", hour: "2-digit", minute: "2-digit" }) });
        renderInbox();
        updateCounts();
        showToast("Добавлено во входящие", "success");
        captureInput.value = "";
      }
    });
  }

  const cmdInput = document.querySelector(".topbar__command-input");
  if (cmdInput) {
    cmdInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && cmdInput.value.trim()) {
        showToast(`Команда: ${cmdInput.value}`, "info");
        cmdInput.value = "";
      }
    });
  }

  document.querySelectorAll('[data-action="focus-start"]').forEach(btn => {
    btn.addEventListener("click", () => showToast("Фокус-режим активирован (25:00) — демо", "info"));
  });

  document.querySelectorAll('[data-action="focus-skip"]').forEach(btn => {
    btn.addEventListener("click", () => showToast("Задача пропущена", "info"));
  });

  document.querySelectorAll(".assistant__action-btn").forEach(btn => {
    btn.addEventListener("click", () => {
      const action = btn.dataset.action;
      const labels = {
        "create-task": "Создание задачи (демо)",
        "reschedule": "Перепланирование (демо)",
        "summarize": "Суммаризация дня (демо)",
        "plan-tomorrow": "План на завтра (демо)"
      };
      showToast(labels[action] || "Действие (демо)", "info");
    });
  });

  document.querySelectorAll('[data-action="voice"]').forEach(btn => {
    btn.addEventListener("click", () => showToast("Голосовой ввод (демо)", "info"));
  });

  document.querySelectorAll('[data-action="notifications"]').forEach(btn => {
    btn.addEventListener("click", () => showToast("Уведомления (демо)", "info"));
  });

  document.querySelectorAll('[data-action="settings"]').forEach(btn => {
    btn.addEventListener("click", () => showToast("Настройки (демо)", "info"));
  });

  initKeyboardNav();
}

function init() {
  renderToday();
  renderInbox();
  renderAllTasks();
  renderAssistant();
  initEventHandlers();
  updateAssistantContext("today");

  setInterval(() => {
    updateAssistantContext(document.body.dataset.screen || "today");
  }, 60000);
}

document.addEventListener("DOMContentLoaded", init);

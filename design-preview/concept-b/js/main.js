// Concept B: Intelligent Command Center — Data & Interactions

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

function formatDate(dateStr) {
  if (!dateStr) return "—";
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
}

function renderToday() {
  const timeline = document.getElementById("today-timeline");
  if (timeline) {
    const todayTasks = TASKS.filter(t => t.status !== "completed").slice(0, 6);
    timeline.innerHTML = todayTasks.map(task => `
      <div class="timeline__item">
        <span class="timeline__time">${task.deadline && task.deadline.includes("T") ? task.deadline.split("T")[1].substring(0,5) : "—"}</span>
        <span class="timeline__dot timeline__dot--${task.priority}"></span>
        <span class="timeline__label">${task.title}</span>
      </div>
    `).join('');
  }
  
  const grid = document.getElementById("today-grid");
  if (grid) {
    const activeTasks = TASKS.filter(t => t.status !== "completed");
    grid.innerHTML = activeTasks.map(task => `
      <div class="task-card ${task.status === 'overdue' ? 'is-overdue' : ''} ${task.status === 'completed' ? 'is-completed' : ''}" data-task-id="${task.id}">
        <div class="task-card__header">
          <h3 class="task-card__title">${task.title}</h3>
          <div class="task-card__checkbox ${task.status === 'completed' ? 'is-checked' : ''}" data-task-id="${task.id}" role="checkbox" aria-checked="${task.status === 'completed'}" tabindex="0"></div>
        </div>
        ${task.note ? `<p class="task-card__note">${task.note}</p>` : ''}
        <div class="task-card__meta">
          <span class="task-card__project">${task.project}</span>
          ${task.deadline ? `<span class="task-card__deadline ${task.status === 'overdue' ? 'is-overdue' : (task.deadline === '2026-11-09' || task.deadline === '2026-11-09T14:00' ? 'is-today' : '')}">${formatDate(task.deadline)}</span>` : ''}
        </div>
        ${task.tags.length ? `<div class="task-card__tags">${task.tags.map(t => `<span class="task-card__tag">#${t}</span>`).join('')}</div>` : ''}
      </div>
    `).join('');
  }
  
  // Focus card
  const focusTask = TASKS.find(t => t.priority === "high" && t.status === "active");
  if (focusTask) {
    document.getElementById("focus-title").textContent = focusTask.title;
    document.getElementById("focus-meta").textContent = `${focusTask.project} · ${getPriorityLabel(focusTask.priority)} · ${focusTask.time}`;
  }
}

function renderInbox() {
  const stream = document.getElementById("inbox-stream");
  if (!stream) return;
  stream.innerHTML = INBOX.map((item, i) => `
    <div class="inbox-card" data-inbox-index="${i}">
      <div class="inbox-card__checkbox" data-inbox-index="${i}" role="checkbox" aria-checked="false" tabindex="0"></div>
      <div class="inbox-card__content">
        <p class="inbox-card__text">${item.text}</p>
        <div class="inbox-card__meta">
          <span>${item.time}</span>
          <span>· быстрый ввод</span>
        </div>
      </div>
      <div class="inbox-card__actions">
        <button class="btn btn--ghost btn--icon" aria-label="В задачу" data-action="inbox-to-task" data-inbox-index="${i}"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M8 2v12M2 8h12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg></button>
        <button class="btn btn--ghost btn--icon" aria-label="Удалить" data-action="inbox-dismiss" data-inbox-index="${i}"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg></button>
      </div>
    </div>
  `).join('');
}

function renderAllTasks() {
  const view = document.getElementById("tasks-view");
  if (!view) return;
  
  const projectFilter = document.getElementById("filter-project")?.value || "";
  const statusFilter = document.getElementById("filter-status")?.value || "";
  const priorityFilter = document.getElementById("filter-priority")?.value || "";
  
  let filtered = TASKS.filter(t => {
    if (projectFilter && t.project !== projectFilter) return false;
    if (statusFilter && t.status !== statusFilter) return false;
    if (priorityFilter && t.priority !== priorityFilter) return false;
    return true;
  });
  
  view.innerHTML = `<div class="tasks-view__grid">${filtered.map(task => `
    <div class="task-row ${task.status === 'overdue' ? 'is-overdue' : ''} ${task.status === 'completed' ? 'is-completed' : ''}" data-task-id="${task.id}">
      <h3 class="task-row__title">${task.title}</h3>
      <div class="task-row__meta">
        <span class="priority-dot priority-dot--${task.priority}"></span>
        <span>${task.project}</span>
        ${task.deadline ? `<span>· ${formatDate(task.deadline)}</span>` : ''}
        <span class="task-row__status task-row__status--${task.status}">${task.status === 'active' ? 'Активна' : task.status === 'overdue' ? 'Просрочена' : 'Выполнена'}</span>
      </div>
    </div>
  `).join('')}</div>`;
  
  // Populate project filter
  const projectSelect = document.getElementById("filter-project");
  if (projectSelect && projectSelect.options.length <= 1) {
    PROJECTS.forEach(p => {
      const opt = document.createElement("option");
      opt.value = p; opt.textContent = p;
      projectSelect.appendChild(opt);
    });
  }
}

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
  container.scrollTop = container.scrollHeight;
}

function initEventHandlers() {
  // Screen switching
  document.querySelectorAll(".sidebar__item, .mobile-nav__item").forEach(el => {
    el.addEventListener("click", () => {
      const screen = el.dataset.screen;
      if (screen) switchScreen(screen);
    });
  });
  
  // Task checkbox toggle
  document.addEventListener("click", (e) => {
    const cb = e.target.closest(".task-card__checkbox");
    if (cb) {
      const taskId = parseInt(cb.dataset.taskId);
      const task = TASKS.find(t => t.id === taskId);
      if (task) {
        task.status = task.status === "completed" ? "active" : "completed";
        renderToday();
        showToast(task.status === "completed" ? "Готово!" : "Восстановлено", "success");
      }
    }
  });
  
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
      setTimeout(() => {
        ASSISTANT_MESSAGES.push({role:"assistant", content: `Понял: «${text}». Обрабатываю…\n\nСоздал задачу «${text}». Приоритет средний, дедлайн сегодня. Изменить?`});
        renderAssistant();
      }, 600);
      input.value = ""; input.style.height = "auto";
      renderAssistant();
    });
  }
  
  // Quick prompts
  document.querySelectorAll(".quick-prompt").forEach(btn => {
    btn.addEventListener("click", () => {
      const input = document.getElementById("assistant-input");
      if (input) { input.value = btn.dataset.prompt; input.focus(); }
    });
  });
  
  // Filter changes
  ["filter-project", "filter-status", "filter-priority"].forEach(id => {
    const el = document.getElementById(id);
    if (el) el.addEventListener("change", renderAllTasks);
  });
  
  // Inbox actions
  document.addEventListener("click", (e) => {
    const btn = e.target.closest('[data-action="inbox-to-task"]');
    if (btn) showToast("Задача создана из входящего", "success");
    const dismiss = e.target.closest('[data-action="inbox-dismiss"]');
    if (dismiss) showToast("Удалено", "info");
  });
  
  // Capture bar
  const captureInput = document.querySelector(".capture-bar__input");
  if (captureInput) {
    captureInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && captureInput.value.trim()) {
        showToast("Добавлено во входящие", "success");
        captureInput.value = "";
      }
    });
  }
  
  // Command bar
  const cmdInput = document.querySelector(".topbar__command-input");
  if (cmdInput) {
    cmdInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && cmdInput.value.trim()) {
        showToast(`Команда: ${cmdInput.value}`, "info");
        cmdInput.value = "";
      }
    });
  }
  
  // Focus mode
  document.querySelectorAll('[data-action="focus-start"]').forEach(btn => {
    btn.addEventListener("click", () => showToast("Фокус-режим активирован (25:00)", "info"));
  });
}

function init() {
  renderToday();
  renderInbox();
  renderAllTasks();
  renderAssistant();
  initEventHandlers();
}

document.addEventListener("DOMContentLoaded", init);

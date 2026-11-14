<script lang="ts">
	import { onDestroy, onMount, tick } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import {
		completeTask,
		confirmPendingAction,
		getConversation,
		getDocument,
		listDocuments,
		getStatus,
		getSummary,
		getTask,
		listConversations,
		listTasks,
		rejectPendingAction,
		sendAssistantMessage,
		uploadDocument,
		reprocessDocument,
		type AssistantMessage,
		type PendingAction,
		type Attachment,
		type DocumentTask,
		type Summary,
		type SystemStatus,
		type UploadedDocument,
		type Task
	} from '$lib/api';
	import { errorMessage, formatDate, formatLongDate, formatTime, priorityLabel, todayISO } from '$lib/format';
	import { bumpConversations, liveRevision } from '$lib/live';
	import { profile as profileStore, loadProfile } from '$lib/profile';
	import { renderMarkdown } from '$lib/markdown';
	import {
		getAssistantConfig,
		updateAssistantConfig,
		type AssistantModel,
		type StreamTool
	} from '$lib/stream';
	import { assistantRun, type RunState } from '$lib/assistantRun';
	import {
		ArrowUp,
		Brain,
		CalendarClock,
		Check,
		ChevronDown,
		Copy,
		FileText,
		ChevronRight,
		CornerUpRight,
		Image as ImageIcon,
		Mic,
		Paperclip,
		RefreshCw,
		Search,
		Sparkles,
		Square
	} from 'lucide-svelte';

	export let basePath = '/';

	interface Turn {
		id: number;
		role: 'user' | 'assistant';
		content: string;
		reasoning: string;
		created_at: string;
		cards: Task[];
		actions: PendingAction[];
		attachments: Attachment[];
	}

	type Row =
		| { kind: 'divider'; key: string; label: string }
		| { kind: 'turn'; turn: Turn };

	const defaultSuggestions = [
		'Что у меня на сегодня?',
		'Разбери входящие',
		'Создай задачу на завтра',
		'Помоги расставить приоритеты'
	];

	let conversationID = 0;
	let messages: AssistantMessage[] = [];
	let pending: PendingAction[] = [];
	let input = '';
	let loading = true;
	let sending = false;
	let busyAction: number | null = null;
	let planBusyId: number | null = null;
	let error = '';
	let bootstrapped = false;
	let bootstrapping: Promise<void> | null = null;
	let handledParam = '';
	let skipBootstrap = false;
	let summary: Summary | null = null;
	let status: SystemStatus | null = null;
	let weekTasks: Task[] = [];
	let contextLoading = true;
	let contextError = '';
	let refreshing = false;
	let messagesEl: HTMLDivElement;
	let textareaEl: HTMLTextAreaElement;
	let menuEl: HTMLDetailsElement;
	let lastScreen = basePath;
	let searchQuery = '';
	let document: UploadedDocument | null = null;
	let documentOpen = false;
	let documentUploading = false;
	let documentUploadingName = '';
	let documentError = '';
	let documentInput: HTMLInputElement | undefined;
	let imageInput: HTMLInputElement | undefined;
	let documentTimer: ReturnType<typeof setInterval> | undefined;
	let documentPolling = false;

	let models: AssistantModel[] = [];
	let selectedModel = '';
	let modelMenuOpen = false;
	let modelPickerEl: HTMLDivElement | undefined;
	let modelTriggerEl: HTMLButtonElement | undefined;
	let autoConfirm = false;
	let streaming = false;
	let streamContent = '';
	let streamReasoning = '';
	let activeTools: Array<{ name: string; status: 'running' | 'done' }> = [];
	let localUserContent = '';
	let localUserAt = '';
	let attachedDocuments: UploadedDocument[] = [];
	let localDocuments: UploadedDocument[] = [];
	let lastSendContent = '';
	let streamError = '';
	let providerNotice = '';
	let copiedId = 0;
	let copyTimer: ReturnType<typeof setTimeout> | undefined;
	let showScroll = false;

	const dayMonthFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });
	const shortMonthFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short' });
	const weekdayFmt = new Intl.DateTimeFormat('ru-RU', { weekday: 'short' });

	$: origin = $page.url.searchParams.get('from');
	$: turns = buildTurns(messages, pending);
	$: visibleTurns = filterTurns(turns, searchQuery);
	$: rows = withDividers(visibleTurns);

	$: doneToday = summary?.stats.completed_today ?? 0;
	$: totalToday = (summary?.stats.active_total ?? 0) + doneToday;
	$: progress = totalToday > 0 ? Math.round((doneToday / totalToday) * 100) : 0;

	$: planItems = mergePlan(summary);
	$: nextDeadline = nextDeadlineOf(summary);
	$: weekRange = formatWeekRange();
	$: weekDays = buildWeekDays();
	$: weekRows = [...weekTasks].sort((a, b) => (taskDate(a) ?? '').localeCompare(taskDate(b) ?? '')).slice(0, 5);
	$: online = !contextError && (status ? status.ok : true);
	$: selectedModelLabel =
		models.find((model) => model.id === selectedModel)?.name ?? (models.length ? 'Модель' : 'По умолчанию');
	$: screenLabel = origin && origin !== basePath ? screenName(origin) : 'ассистент';
	$: lastAssistantId = [...turns].reverse().find((turn) => turn.role === 'assistant')?.id ?? 0;

	function plural(count: number, one: string, few: string, many: string): string {
		const mod10 = count % 10;
		const mod100 = count % 100;
		if (mod10 === 1 && mod100 !== 11) return one;
		if (mod10 >= 2 && mod10 <= 4 && (mod100 < 10 || mod100 >= 20)) return few;
		return many;
	}

	function taskWord(count: number): string {
		return plural(count, 'задача', 'задачи', 'задач');
	}

	function screenName(path: string): string {
		if (path === '/today') return 'сегодня';
		if (path === '/inbox') return 'входящие';
		if (path === '/tasks') return 'задачи';
		if (path === '/calendar') return 'календарь';
		return path.replace(/^\//, '') || 'ассистент';
	}

	function isoKey(date: Date): string {
		const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
		return local.toISOString().slice(0, 10);
	}

	function addDaysISO(days: number): string {
		const date = new Date();
		date.setDate(date.getDate() + days);
		return isoKey(date);
	}

	function taskDate(task: Task): string | null {
		if (task.due_date) return task.due_date;
		if (task.planned_date) return task.planned_date;
		if (task.due_at) return task.due_at.slice(0, 10);
		return null;
	}

	function dayKey(value: string): string {
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) return '';
		return isoKey(date);
	}

	function dayLabel(value: string): string {
		const key = dayKey(value);
		if (key === todayISO()) {
			return `Сегодня, ${dayMonthFmt.format(new Date(value))}`;
		}
		return formatLongDate(new Date(value));
	}

	function formatWeekRange(): string {
		const start = new Date();
		const end = new Date();
		end.setDate(start.getDate() + 6);
		if (start.getMonth() === end.getMonth()) {
			return `${start.getDate()}–${end.getDate()} ${dayMonthFmt.format(end).replace(/^\d+\s*/, '')}`;
		}
		return `${shortMonthFmt.format(start)} — ${shortMonthFmt.format(end)}`;
	}

	function formatShortDay(key: string): string {
		const date = new Date(`${key}T00:00:00`);
		const text = weekdayFmt.format(date);
		return text.charAt(0).toUpperCase() + text.slice(1);
	}

	function asTask(value: unknown): Task | null {
		if (!value || typeof value !== 'object') return null;
		const raw = value as Record<string, unknown>;
		if (typeof raw.id !== 'number' || typeof raw.title !== 'string' || typeof raw.status !== 'string') {
			return null;
		}
		return {
			id: raw.id,
			title: raw.title,
			note: typeof raw.note === 'string' ? raw.note : null,
			priority: (raw.priority as Task['priority']) ?? 'medium',
			status: raw.status as Task['status'],
			planned_date: typeof raw.planned_date === 'string' ? raw.planned_date : null,
			due_date: typeof raw.due_date === 'string' ? raw.due_date : null,
			due_at: typeof raw.due_at === 'string' ? raw.due_at : null,
			timezone: typeof raw.timezone === 'string' ? raw.timezone : 'Europe/Moscow',
			tags: Array.isArray(raw.tags) ? (raw.tags as string[]) : [],
			created_at: '',
			updated_at: '',
			completed_at: null,
			cancelled_at: null,
			overdue: Boolean(raw.overdue),
			version: typeof raw.version === 'number' ? raw.version : 0
		};
	}

	function extractTasks(content: string): Task[] {
		try {
			const data = JSON.parse(content);
			const list = Array.isArray(data)
				? data
				: data && typeof data === 'object' && Array.isArray((data as Record<string, unknown>).tasks)
					? ((data as Record<string, unknown>).tasks as unknown[])
					: [data];
			return list.map(asTask).filter((task): task is Task => task !== null);
		} catch {
			return [];
		}
	}

	function attachmentTasks(attachments: Attachment[] | undefined): Task[] {
		if (!attachments || attachments.length === 0) return [];
		const result: Task[] = [];
		for (const attachment of attachments) {
			if (attachment.kind === 'task') {
				const task = asTask(attachment.data);
				if (task) result.push(task);
				continue;
			}
			if (attachment.kind === 'task_list') {
				const data = attachment.data as Record<string, unknown> | unknown[] | null;
				const list = Array.isArray(data)
					? data
					: data && Array.isArray((data as Record<string, unknown>).tasks)
						? ((data as Record<string, unknown>).tasks as unknown[])
						: [];
				for (const item of list) {
					const task = asTask(item);
					if (task) result.push(task);
				}
			}
		}
		return result;
	}

	function uniqueTasks(tasks: Task[]): Task[] {
		const seen = new Set<number>();
		const result: Task[] = [];
		for (const task of tasks) {
			if (seen.has(task.id)) continue;
			seen.add(task.id);
			result.push(task);
		}
		return result;
	}

	function buildTurns(list: AssistantMessage[], actions: PendingAction[]): Turn[] {
		const result: Turn[] = [];
		let queued: Task[] = [];

		for (const message of list) {
			if (message.role === 'tool') {
				queued.push(...extractTasks(message.content));
				continue;
			}
			if (!message.content.trim()) continue;

			const attached = message.role === 'assistant' ? attachmentTasks(message.attachments) : [];
			const cards = message.role === 'assistant' ? uniqueTasks(attached.length > 0 ? attached : queued) : [];
			if (message.role === 'assistant') queued = [];

			result.push({
				id: message.id,
				role: message.role,
				content: message.content,
				reasoning: message.reasoning ?? '',
				created_at: message.created_at,
				cards,
				actions: [],
				attachments: message.attachments ?? []
			});
		}

		if (queued.length > 0) {
			const lastAssistant = [...result].reverse().find((turn) => turn.role === 'assistant');
			if (lastAssistant) lastAssistant.cards = uniqueTasks([...lastAssistant.cards, ...queued]);
		}

		const lastAssistant = [...result].reverse().find((turn) => turn.role === 'assistant');
		if (lastAssistant) {
			lastAssistant.actions = actions.filter((action) => action.status === 'pending');
		}

		return result;
	}

	function filterTurns(list: Turn[], query: string): Turn[] {
		const needle = query.trim().toLowerCase();
		if (!needle) return list;
		return list.filter(
			(turn) =>
				turn.content.toLowerCase().includes(needle) ||
				turn.cards.some((card) => card.title.toLowerCase().includes(needle))
		);
	}

	function withDividers(list: Turn[]): Row[] {
		const rows: Row[] = [];
		let previous = '';
		for (const turn of list) {
			const key = dayKey(turn.created_at);
			if (key && key !== previous) {
				rows.push({ kind: 'divider', key, label: dayLabel(turn.created_at) });
				previous = key;
			}
			rows.push({ kind: 'turn', turn });
		}
		return rows;
	}

	function mergePlan(value: Summary | null) {
		if (!value) return [];
		const seen = new Set<number>();
		const items = [...value.today, ...value.overdue];
		return items.filter((item) => {
			if (seen.has(item.id)) return false;
			seen.add(item.id);
			return true;
		}).slice(0, 6);
	}

	function nextDeadlineOf(value: Summary | null): string | null {
		if (!value) return null;
		const times = value.today
			.map((item) => item.time)
			.filter((time): time is string => Boolean(time) && time !== '—')
			.sort();
		return times[0] ?? null;
	}

	function buildWeekDays() {
		return Array.from({ length: 7 }, (_, index) => {
			const key = addDaysISO(index);
			return {
				key,
				label: formatShortDay(key),
				date: new Date(`${key}T00:00:00`).getDate(),
				count: weekTasks.filter((task) => taskDate(task) === key).length,
				today: key === todayISO()
			};
		});
	}

	function cardBadge(task: Task): { label: string; tone: 'accent' | 'success' } | null {
		const key = taskDate(task);
		if (key === todayISO()) return { label: 'Сегодня', tone: 'success' };
		if (key === addDaysISO(1)) return { label: 'Завтра', tone: 'accent' };
		if (task.tags.some((tag) => tag.toLowerCase() === 'фокус')) return { label: 'Фокус', tone: 'accent' };
		if (task.priority === 'high') return { label: 'Важно', tone: 'accent' };
		return null;
	}

	function cardMeta(task: Task): string {
		const parts: string[] = [];
		const key = taskDate(task);
		if (key === todayISO()) parts.push('Сегодня');
		else if (key === addDaysISO(1)) parts.push('Завтра');
		else if (key) parts.push(formatDate(key));
		if (task.due_at) parts.push(formatTime(task.due_at));
		parts.push(priorityLabel(task.priority).toLowerCase());
		return parts.join(' · ');
	}

	function turnSuggestions(turn: Turn): Array<{ label: string; prompt: string }> {
		const [first] = turn.cards;
		if (first) {
			if (/отч[её]т|тезис|презентац/i.test(first.title)) {
				return [
					{ label: 'Открыть отчёт', prompt: `Открой задачу «${first.title}»` },
					{ label: 'Подготовить тезисы', prompt: `Подготовь тезисы по задаче «${first.title}»` }
				];
			}
			return [
				{ label: 'Перенести на завтра', prompt: `Перенеси задачу «${first.title}» на завтра` },
				{ label: 'Добавить напоминание', prompt: `Добавь напоминание по задаче «${first.title}»` }
			];
		}
		if (turn.role === 'assistant' && !searchQuery) {
			return [
				{ label: 'Что у меня сегодня?', prompt: 'Что у меня на сегодня?' },
				{ label: 'Разбери входящие', prompt: 'Разбери входящие' }
			];
		}
		return [];
	}

	function renderMessage(content: string): string {
		return renderMarkdown(content);
	}

	function isNearBottom(): boolean {
		if (!messagesEl) return true;
		return messagesEl.scrollHeight - messagesEl.clientHeight - messagesEl.scrollTop < 140;
	}

	async function scrollIfNearBottom() {
		if (!isNearBottom()) return;
		await tick();
		if (messagesEl) {
			messagesEl.scrollTop = messagesEl.scrollHeight;
			showScroll = false;
		}
	}

	function handleScroll() {
		if (!messagesEl) return;
		showScroll = messagesEl.scrollHeight - messagesEl.clientHeight - messagesEl.scrollTop > 180;
	}

	async function scrollToBottom() {
		await tick();
		messagesEl?.scrollTo({
			top: messagesEl.scrollHeight,
			behavior: prefersReducedMotion() ? 'auto' : 'smooth'
		});
		showScroll = false;
	}

	function prefersReducedMotion(): boolean {
		return typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
	}

	// A dropped SSE body rejects with "network error" / "Failed to fetch". That is
	// a transport failure, not an assistant answer, so say so plainly.
	function isConnectionError(err: unknown): boolean {
		if (err instanceof TypeError) return true;
		const message = err instanceof Error ? err.message.toLowerCase() : '';
		return (
			message.includes('network error') ||
			message.includes('failed to fetch') ||
			message.includes('networkerror') ||
			message.includes('load failed') ||
			message.includes('err_connection')
		);
	}

	async function copyTurn(id: number, content: string) {
		try {
			await navigator.clipboard.writeText(content);
			copiedId = id;
			clearTimeout(copyTimer);
			copyTimer = setTimeout(() => (copiedId = 0), 1600);
		} catch {
			error = 'Не удалось скопировать текст';
		}
	}

	function stopGeneration() {
		assistantRun.stop();
	}

	async function loadAssistantConfig() {
		try {
			const config = await getAssistantConfig();
			models = config.models;
			selectedModel = config.model;
			autoConfirm = config.auto_confirm;
		} catch {
			// Keep defaults if the catalog is unavailable.
		}
	}

	async function saveAssistantConfig() {
		try {
			const config = await updateAssistantConfig({ model: selectedModel, auto_confirm: autoConfirm });
			models = config.models;
			selectedModel = config.model;
			autoConfirm = config.auto_confirm;
		} catch (err) {
			error = errorMessage(err);
		}
	}

	function closeModelMenu(restoreFocus = false) {
		if (!modelMenuOpen) return;
		modelMenuOpen = false;
		if (restoreFocus) modelTriggerEl?.focus();
	}

	async function openModelMenu() {
		modelMenuOpen = true;
		await tick();
		focusModelOption(Math.max(0, models.findIndex((model) => model.id === selectedModel)));
	}

	function toggleModelMenu() {
		if (modelMenuOpen) closeModelMenu();
		else void openModelMenu();
	}

	function focusModelOption(index: number) {
		const options = modelPickerEl?.querySelectorAll<HTMLButtonElement>('[role="option"]');
		if (!options || options.length === 0) return;
		options[Math.max(0, Math.min(index, options.length - 1))]?.focus();
	}

	function handleModelTriggerKey(event: KeyboardEvent) {
		if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
			event.preventDefault();
			void openModelMenu();
			return;
		}
		if (event.key === 'Escape') closeModelMenu();
	}

	function handleModelOptionKey(event: KeyboardEvent, index: number) {
		switch (event.key) {
			case 'ArrowDown':
				event.preventDefault();
				focusModelOption(index + 1);
				break;
			case 'ArrowUp':
				event.preventDefault();
				focusModelOption(index - 1);
				break;
			case 'Home':
				event.preventDefault();
				focusModelOption(0);
				break;
			case 'End':
				event.preventDefault();
				focusModelOption(models.length - 1);
				break;
			case 'Escape':
				event.preventDefault();
				closeModelMenu(true);
				break;
			case 'Tab':
				closeModelMenu();
				break;
		}
	}

	function selectModel(id: string) {
		closeModelMenu(true);
		if (id === selectedModel) return;
		selectedModel = id;
		void saveAssistantConfig();
	}

	function handleWindowClick(event: MouseEvent) {
		if (!modelMenuOpen) return;
		const target = event.target as Node | null;
		if (target && modelPickerEl?.contains(target)) return;
		closeModelMenu();
	}

	function toolLabel(name: string): string {
		if (name === 'get_task' || name === 'list_tasks' || name === 'search') return 'Проверяю задачи';
		if (name === 'create_task') return 'Создаю задачу';
		if (name === 'update_task') return 'Обновляю задачу';
		if (name === 'complete_task') return 'Завершаю задачу';
		if (name === 'reschedule_task') return 'Переношу задачу';
		if (name === 'capture_inbox') return 'Сохраняю во входящие';
		if (name === 'list_events' || name === 'get_event') return 'Проверяю календарь';
		if (name === 'create_event') return 'Создаю событие';
		if (name === 'create_events') return 'Создаю события';
		if (name === 'update_event') return 'Изменяю событие';
		if (name === 'delete_event') return 'Удаляю событие';
		if (name === 'get_today_summary') return 'Смотрю сводку дня';
		if (name === 'list_insights') return 'Проверяю наблюдения';
		if (name === 'read_document') return 'Читаю документ';
		if (name === 'read_file') return 'Читаю файл';
		if (name === 'list_files') return 'Смотрю каталог';
		if (name === 'search_files') return 'Ищу в файлах';
		if (name === 'write_file') return 'Записываю файл';
		if (name === 'edit_file') return 'Правлю файл';
		if (name === 'delete_path') return 'Удаляю путь';
		if (name === 'run_command') return 'Выполняю команду';
		return 'Выполняю действие';
	}

	async function scrollToLatest() {
		await tick();
		const element = messagesEl;
		if (element) element.scrollTop = element.scrollHeight;
	}

	async function loadConversation() {
		if (!conversationID) {
			messages = [];
			pending = [];
			return;
		}
		const view = await getConversation(conversationID);
		messages = view.messages;
		pending = view.pending_actions.filter((action) => action.status === 'pending');
	}

	async function loadContext() {
		try {
			summary = await getSummary();
			contextError = '';
		} catch (err) {
			contextError = errorMessage(err);
		} finally {
			contextLoading = false;
		}
	}

	async function loadWeek() {
		try {
			const result = await listTasks({ limit: 100, sort: 'due_at' });
			const from = todayISO();
			const to = addDaysISO(6);
			weekTasks = result.data.filter((task) => {
				const key = taskDate(task);
				return task.status === 'open' && Boolean(key) && key! >= from && key! <= to;
			});
		} catch {
			weekTasks = [];
		}
	}

	async function bootstrap() {
		if (bootstrapped) return;
		if (bootstrapping) return bootstrapping;
		bootstrapping = (async () => {
			loading = true;
			try {
				if (assistantRun.conversationID) {
					conversationID = assistantRun.conversationID;
					await loadConversation();
					error = '';
					bootstrapped = true;
					return;
				}
				const list = await listConversations();
				if (skipBootstrap) return;
				const latest = list.data[0];
				if (latest) {
					conversationID = latest.id;
					await loadConversation();
				}
				error = '';
				bootstrapped = true;
			} catch (err) {
				error = errorMessage(err);
			} finally {
				loading = false;
				bootstrapping = null;
			}
			await scrollToLatest();
		})();
		return bootstrapping;
	}

	async function openConversation(id: number) {
		if (!Number.isFinite(id) || id <= 0) return;
		loading = true;
		bootstrapped = true;
		skipBootstrap = true;
		searchQuery = '';
		try {
			conversationID = id;
			await loadConversation();
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
		await tick();
		await scrollToLatest();
		await goto(basePath, { replaceState: true, noScroll: true });
		handledParam = '';
	}

	async function handleQuery(query: string) {
		await bootstrap();
		input = query;
		const from = $page.url.searchParams.get('from') || basePath;
		await send(from);
		await goto(basePath, { replaceState: true, noScroll: true });
		handledParam = '';
	}

	$: {
		const params = $page.url.searchParams;
		const query = params.get('q');
		const conversation = params.get('conversation');
		const fresh = params.get('new');
		const key = `${query ?? ''}|${conversation ?? ''}|${fresh ?? ''}`;
		if (key !== handledParam && (query || conversation || fresh)) {
			handledParam = key;
			if (query) void handleQuery(query);
			else if (conversation) void openConversation(Number(conversation));
			else if (fresh) newConversation();
		}
	}

	async function send(screen = basePath) {
		const content = input.trim();
		if (!content || assistantRun.running) return;
		lastScreen = screen;
		lastSendContent = content;
		error = '';
		const documents = [...attachedDocuments];
		const documentIDs = documents.map((doc) => doc.id);
		attachedDocuments = [];
		input = '';
		// The turn belongs to the store, not to this component: switching to
		// another section unmounts the chat and must not cancel the answer.
		assistantRun.start({
			content,
			conversation_id: conversationID,
			model: selectedModel || undefined,
			auto_confirm: autoConfirm,
			document_ids: documentIDs,
			documents,
			interaction: { screen }
		});
		await scrollToLatest();
	}

	function applyRunState(state: RunState) {
		sending = state.active;
		streaming = state.active;
		streamContent = state.content;
		streamReasoning = state.reasoning;
		activeTools = state.tools;
		providerNotice = state.notice;
		localUserContent = state.userContent;
		localUserAt = state.userAt;
		localDocuments = state.documents;
		if (state.active) {
			pending = [...state.pending];
			void scrollIfNearBottom();
		} else if (state.error) {
			error = state.error;
		}
	}

	async function finishRun(state: RunState) {
		const shouldStick = isNearBottom();
		if (state.conversationID && (!conversationID || conversationID === state.conversationID)) {
			conversationID = state.conversationID;
		}
		bumpConversations();
		sending = false;
		streaming = false;
		try {
			if (conversationID) await loadConversation();
		} catch (err) {
			error = errorMessage(err);
		}
		await Promise.all([loadContext(), loadWeek()]);
		if (state.error) error = state.error;
		streamContent = '';
		streamReasoning = '';
		providerNotice = '';
		activeTools = [];
		localUserContent = '';
		localDocuments = [];
		assistantRun.clear();
		if (shouldStick) await scrollToLatest();
	}

	async function confirm(action: PendingAction) {
		busyAction = action.id;
		error = '';
		try {
			await confirmPendingAction(action.id);
			await loadConversation();
			await Promise.all([loadContext(), loadWeek()]);
			bumpConversations();
			await scrollToLatest();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyAction = null;
		}
	}

	async function reject(action: PendingAction) {
		busyAction = action.id;
		error = '';
		try {
			await rejectPendingAction(action.id);
			await loadConversation();
			await scrollToLatest();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyAction = null;
		}
	}

	async function completePlan(id: number) {
		if (planBusyId !== null) return;
		planBusyId = id;
		error = '';
		try {
			const task = await getTask(id);
			await completeTask(task.id, task.version);
			await Promise.all([loadContext(), loadWeek()]);
		} catch (err) {
			error = errorMessage(err);
		} finally {
			planBusyId = null;
		}
	}

	async function refreshAll() {
		if (menuEl) menuEl.open = false;
		refreshing = true;
		try {
			await Promise.all([loadContext(), loadWeek(), loadStatus()]);
			if (conversationID && !sending) await loadConversation();
		} finally {
			refreshing = false;
		}
	}

	async function loadStatus() {
		try {
			status = await getStatus();
		} catch {
			status = null;
		}
	}

	function retry() {
		error = '';
		if (!bootstrapped) {
			bootstrapped = false;
			void bootstrap();
			void loadContext();
			void loadWeek();
			return;
		}
		const previous = lastSendContent || assistantRun.lastUserContent;
		if (previous) {
			input = previous;
			void send(lastScreen || assistantRun.lastScreen);
		}
	}

	function newConversation() {
		assistantRun.reset();
		skipBootstrap = true;
		searchQuery = '';
		conversationID = 0;
		messages = [];
		pending = [];
		input = '';
		error = '';
		bootstrapped = true;
		loading = false;
		void goto(basePath, { replaceState: true, noScroll: true }).then(() => {
			handledParam = '';
		});
		if (messagesEl) messagesEl.scrollTop = 0;
		streamContent = '';
		streamReasoning = '';
		activeTools = [];
		localUserContent = '';
		attachedDocuments = [];
		localDocuments = [];
		sending = false;
		streaming = false;
	}

	function useSuggestion(text: string) {
		input = text;
		textareaEl?.focus();
	}

	function handleComposerKey(event: KeyboardEvent) {
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			void send();
		}
	}

	function openDocumentPicker() {
		documentInput?.click();
	}

	function openImagePicker() {
		imageInput?.click();
	}

	async function handleDocumentFile(event: Event) {
		const input = event.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		input.value = '';
		if (!file) return;
		await uploadFile(file);
	}

	async function uploadFile(file: File) {
		if (file.size > 25 * 1024 * 1024) {
			documentError = 'Файл больше 25 МБ';
			documentOpen = true;
			return;
		}
		documentUploading = true;
		documentUploadingName = file.name;
		documentError = '';
		document = null;
		documentOpen = true;
		try {
			const created = await uploadDocument(file);
			document = created;
			startDocumentPolling(created.id);
		} catch (err) {
			documentError = errorMessage(err);
		} finally {
			documentUploading = false;
		}
	}

	function startDocumentPolling(id: number) {
		stopDocumentPolling();
		void pollDocument(id);
		documentTimer = setInterval(() => void pollDocument(id), 1500);
	}

	async function pollDocument(id: number) {
		if (documentPolling) return;
		documentPolling = true;
		try {
			const next = await getDocument(id);
			if (!document || document.id !== id) return;
			document = next;
			if (next.status === 'processed') {
				stopDocumentPolling();
			}
			if (next.status === 'failed') stopDocumentPolling();
		} catch (err) {
			documentError = errorMessage(err);
			stopDocumentPolling();
		} finally {
			documentPolling = false;
		}
	}

	function stopDocumentPolling() {
		if (documentTimer) {
			clearInterval(documentTimer);
			documentTimer = undefined;
		}
	}

	async function openCurrentDocument() {
		if (document) {
			documentOpen = true;
			if (document.status === 'pending' || document.status === 'processing') {
				startDocumentPolling(document.id);
			}
			return;
		}
		try {
			const list = await listDocuments(1, 0);
			const latest = list.data[0];
			if (!latest) {
				openDocumentPicker();
				return;
			}
			const full = await getDocument(latest.id);
			document = full;
			documentOpen = true;
			if (full.status === 'pending' || full.status === 'processing') {
				startDocumentPolling(full.id);
			}
		} catch (err) {
			documentError = errorMessage(err);
			documentOpen = true;
		}
	}

	function closeDocument() {
		documentOpen = false;
		stopDocumentPolling();
	}

	async function retryDocumentProcessing() {
		if (!document) return;
		documentError = '';
		try {
			await reprocessDocument(document.id);
			document = { ...document, status: 'pending', error: null };
			startDocumentPolling(document.id);
		} catch (err) {
			documentError = errorMessage(err);
		}
	}

	function documentFiles(attachments: Attachment[] | undefined): Array<{ id: number; filename: string; size: number }> {
		if (!attachments) return [];
		const result: Array<{ id: number; filename: string; size: number }> = [];
		for (const attachment of attachments) {
			if (attachment.kind !== 'document') continue;
			const data = attachment.data as Record<string, unknown> | null;
			if (!data || typeof data.id !== 'number' || typeof data.filename !== 'string') continue;
			result.push({ id: data.id, filename: data.filename, size: typeof data.size === 'number' ? data.size : 0 });
		}
		return result;
	}

	function attachDocument() {
		if (!document || document.status !== 'processed') return;
		if (!attachedDocuments.some((item) => item.id === document!.id)) {
			attachedDocuments = [...attachedDocuments, document];
		}
		documentOpen = false;
		document = null;
		documentError = '';
		stopDocumentPolling();
	}

	function removeAttachedDocument(id: number) {
		attachedDocuments = attachedDocuments.filter((item) => item.id !== id);
	}

	function documentStatusLabel(status: string): string {
		if (status === 'pending') return 'Загружен';
		if (status === 'processing') return 'Конвертирую в Markdown…';
		if (status === 'processed') return 'Готово';
		return 'Ошибка обработки';
	}

	function documentSize(bytes: number): string {
		if (bytes < 1024) return `${bytes} Б`;
		if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} КБ`;
		return `${(bytes / (1024 * 1024)).toFixed(1)} МБ`;
	}

	onDestroy(stopDocumentPolling);

	onMount(() => {
		const detachRun = assistantRun.attach({ update: applyRunState, finish: finishRun });
		void bootstrap();
		void loadContext();
		void loadWeek();
		void loadStatus();
		void loadProfile();
		void loadAssistantConfig();

		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0) return;
			clearTimeout(timer);
			timer = setTimeout(() => {
				if (sending) return;
				const element = messagesEl;
				const atBottom = element
					? element.scrollHeight - element.clientHeight - element.scrollTop < 80
					: false;
				void loadContext();
				void loadWeek();
				if (conversationID) {
					void loadConversation()
						.then(() => {
							if (atBottom) void scrollToLatest();
						})
						.catch((err) => (error = errorMessage(err)));
				}
			}, 250);
		});

		return () => {
			// Deliberately no abort: the assistant keeps working while the user
			// is on another section, and the next mount re-attaches to the run.
			detachRun();
			unsubscribe();
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>Ассистент — LifeOS</title>
</svelte:head>

<svelte:window on:click={handleWindowClick} />

<div class="assistant-page">
	<section class="chat-column" aria-label="Диалог с ассистентом">
		<header class="chat-header">
			<div class="chat-title">
				<h1>Личный ассистент</h1>
				<p class="sync-line" class:is-offline={!online}>
					<span class="sync-dot" aria-hidden="true"></span>
					{#if online}
						Онлайн · синхронизировано с календарём и задачами
					{:else}
						Нет связи с данными · попробуйте обновить
					{/if}
				</p>
			</div>
			<div class="chat-tools">
				<div class="model-picker" class:open={modelMenuOpen} bind:this={modelPickerEl}>
					<button
						type="button"
						class="model-trigger"
						bind:this={modelTriggerEl}
						aria-haspopup="listbox"
						aria-expanded={modelMenuOpen}
						aria-label={`Модель ассистента: ${selectedModelLabel}`}
						title={selectedModelLabel}
						on:click={toggleModelMenu}
						on:keydown={handleModelTriggerKey}
					>
						<Sparkles size={12} strokeWidth={1.7} aria-hidden="true" />
						<span class="model-name">{selectedModelLabel}</span>
						<span class="model-chevron"><ChevronDown size={12} strokeWidth={2} aria-hidden="true" /></span>
					</button>
					{#if modelMenuOpen}
						<div class="model-pop" role="listbox" aria-label="Доступные модели">
							<p class="model-pop-label">Модель ответа</p>
							{#if models.length === 0}
								<p class="model-empty">Каталог моделей недоступен</p>
							{:else}
								{#each models as model, index (model.id)}
									<button
										type="button"
										role="option"
										aria-selected={model.id === selectedModel}
										class="model-option"
										class:selected={model.id === selectedModel}
										title={model.name}
										on:click={() => selectModel(model.id)}
										on:keydown={(event) => handleModelOptionKey(event, index)}
									>
										<span class="model-option-name">{model.name}</span>
										{#if model.id === selectedModel}
											<Check size={13} strokeWidth={2.2} aria-hidden="true" />
										{/if}
									</button>
								{/each}
							{/if}
						</div>
					{/if}
				</div>
				<label class="search">
					<Search size={13} strokeWidth={1.8} aria-hidden="true" />
					<input bind:value={searchQuery} placeholder="Поиск по диалогу" aria-label="Поиск по диалогу" />
				</label>
				<details class="menu" bind:this={menuEl}>
					<summary aria-label="Меню диалога" title="Меню диалога"><span aria-hidden="true">···</span></summary>
					<div class="menu-pop">
						<button type="button" on:click={newConversation}>Новый диалог</button>
						<button type="button" on:click={refreshAll}>Обновить данные</button>
						<button type="button" on:click={() => { if (menuEl) menuEl.open = false; void openCurrentDocument(); }}>Документы</button>
						<label class="menu-toggle">
							<input type="checkbox" bind:checked={autoConfirm} on:change={saveAssistantConfig} />
							<span>Автоподтверждение</span>
						</label>
						<p class="menu-hint">Изменения выполняются без отдельного подтверждения.</p>
					</div>
				</details>
			</div>
		</header>

		{#if error}
			<div class="error-banner" role="alert">
				<span>{error}</span>
				<div class="error-actions">
					<button class="text-button" type="button" on:click={retry}>Повторить</button>
					<button class="icon-close" type="button" aria-label="Закрыть" on:click={() => (error = '')}>×</button>
				</div>
			</div>
		{/if}

		{#if searchQuery.trim()}
			<div class="search-note">
				{visibleTurns.length > 0
					? `Найдено сообщений: ${visibleTurns.length}`
					: 'Ничего не найдено в этом диалоге'}
			</div>
		{/if}

		<div
			class="chat-scroll"
			bind:this={messagesEl}
			role="log"
			aria-live="polite"
			aria-busy={loading}
			aria-label="Сообщения диалога"
			on:scroll={handleScroll}
		>
			<div class="chat-inner">
				{#if loading}
					<div class="loading-state" aria-label="Загружаю диалог">
						<div class="skeleton-turn">
							<span class="skeleton-line skeleton-line--short"></span>
							<span class="skeleton-line"></span>
							<span class="skeleton-line skeleton-line--medium"></span>
						</div>
						<div class="skeleton-turn skeleton-turn--offset">
							<span class="skeleton-line skeleton-line--medium"></span>
							<span class="skeleton-line"></span>
						</div>
					</div>
				{:else if turns.length === 0 && !sending}
					<div class="empty-state">
						<span class="empty-mark"><Sparkles size={20} strokeWidth={1.6} /></span>
						<h2>Чем займёмся?</h2>
						<p>
							Скажите, что нужно сделать. Ассистент проверит задачи и входящие, а изменения покажет до
							сохранения.
						</p>
						<div class="suggestions">
							{#each defaultSuggestions as suggestion}
								<button type="button" class="suggestion" on:click={() => useSuggestion(suggestion)}>
									{suggestion}
								</button>
							{/each}
						</div>
					</div>
				{:else}
					{#each rows as row (row.kind === 'divider' ? `d-${row.key}` : `t-${row.turn.id}`)}
						{#if row.kind === 'divider'}
							<div class="day-divider"><span>{row.label}</span></div>
						{:else if row.turn.role === 'user'}
							<article class="turn turn--user">
								<div class="user-meta">
									<span>{$profileStore.name.split(' ')[0]}</span>
									<time>{formatTime(row.turn.created_at)}</time>
								</div>
								<div class="user-line">
									<div class="user-bubble">{@html renderMessage(row.turn.content)}</div>
									<span class="avatar avatar--user">{$profileStore.initials}</span>
								</div>
								{#if documentFiles(row.turn.attachments).length > 0}
									<div class="user-docs">
										{#each documentFiles(row.turn.attachments) as file (file.id)}
											<span class="doc-chip"><FileText size={11} strokeWidth={1.8} />{file.filename}</span>
										{/each}
									</div>
								{/if}
							</article>
						{:else}
							<article class="turn">
								<span class="avatar avatar--assistant"><Sparkles size={16} strokeWidth={1.7} /></span>
								<div class="turn-main">
									<div class="turn-meta">
										<span class="author">Ассистент</span>
										<time>{formatTime(row.turn.created_at)}</time>
										<button
											type="button"
											class="copy-button"
											title="Скопировать ответ"
											aria-label="Скопировать ответ"
											on:click={() => copyTurn(row.turn.id, row.turn.content)}
										>
											{#if copiedId === row.turn.id}
												<Check size={12} strokeWidth={2} />
											{:else}
												<Copy size={12} strokeWidth={1.8} />
											{/if}
										</button>
									</div>
									{#if row.turn.reasoning}
										<details class="reasoning">
											<summary><Brain size={12} strokeWidth={1.8} /> Размышления</summary>
											<div class="reasoning-body">{@html renderMessage(row.turn.reasoning)}</div>
										</details>
									{/if}
									<div class="turn-text">{@html renderMessage(row.turn.content)}</div>

									{#each row.turn.cards as task (task.id)}
										<div class="task-card" class:task-card--done={task.status !== 'open'}>
											<span class="task-icon"><CalendarClock size={15} strokeWidth={1.7} /></span>
											<div class="task-copy">
												<span class="task-title">{task.title}</span>
												<span class="task-meta">{cardMeta(task)}</span>
											</div>
											{#if cardBadge(task)}
												<span class="task-badge" data-tone={cardBadge(task)?.tone}>
													{cardBadge(task)?.label}
												</span>
											{/if}
										</div>
									{/each}

									{#each row.turn.actions as action (action.id)}
										<div class="action-card">
											<div class="action-head">
												<span class="action-label">Требует подтверждения</span>
												<code>{action.tool_name}</code>
											</div>
											<p class="action-copy">{action.preview.summary ?? action.tool_name}</p>
											<div class="action-buttons">
												<button
													type="button"
													class="btn-primary"
													disabled={busyAction === action.id}
													on:click={() => confirm(action)}
												>
													Подтвердить
												</button>
												<button
													type="button"
													class="btn-ghost"
													disabled={busyAction === action.id}
													on:click={() => reject(action)}
												>
													Отклонить
												</button>
											</div>
										</div>
									{/each}

									{#if !searchQuery && (row.turn.cards.length > 0 || row.turn.id === lastAssistantId) && turnSuggestions(row.turn).length > 0}
										<div class="chips">
											{#each turnSuggestions(row.turn) as suggestion}
												<button
													type="button"
													class="chip"
													on:click={() => useSuggestion(suggestion.prompt)}
												>
													<CornerUpRight size={12} strokeWidth={1.8} aria-hidden="true" />
													{suggestion.label}
												</button>
											{/each}
										</div>
									{/if}
								</div>
							</article>
						{/if}
					{/each}

					{#if sending}
						{#if localUserContent}
							<article class="turn turn--user">
								<div class="user-meta">
									<span>{$profileStore.name.split(' ')[0]}</span>
									<time>{formatTime(localUserAt)}</time>
								</div>
								<div class="user-line">
									<div class="user-bubble">{@html renderMessage(localUserContent)}</div>
									<span class="avatar avatar--user">{$profileStore.initials}</span>
								</div>
								{#if localDocuments.length > 0}
									<div class="user-docs">
										{#each localDocuments as file (file.id)}
											<span class="doc-chip"><FileText size={11} strokeWidth={1.8} />{file.filename}</span>
										{/each}
									</div>
								{/if}
							</article>
						{/if}
						<article class="turn turn--stream" aria-label="Ассистент отвечает">
							<span class="avatar avatar--assistant"><Sparkles size={16} strokeWidth={1.7} /></span>
							<div class="turn-main">
								<div class="turn-meta">
									<span class="author">Ассистент</span>
									<span class="streaming-label">
										{#if streamContent || streamReasoning}
											печатает…
										{:else}
											думает…
										{/if}
									</span>
								</div>
								{#if providerNotice}
									<div class="provider-notice" role="status">{providerNotice}</div>
								{/if}
								{#if streamReasoning}
									<details class="reasoning" open>
										<summary><Brain size={12} strokeWidth={1.8} /> Размышления</summary>
										<div class="reasoning-body">{@html renderMessage(streamReasoning)}</div>
									</details>
								{/if}
								{#if activeTools.length > 0}
									<div class="tool-activity">
										{#each activeTools as tool, index (index)}
											<div class="tool-row" data-status={tool.status}>
												{#if tool.status === 'running'}
													<span class="tool-spinner" aria-hidden="true"></span>
												{:else}
													<Check size={12} strokeWidth={2.2} aria-hidden="true" />
												{/if}
												<span>{toolLabel(tool.name)}</span>
											</div>
										{/each}
									</div>
								{/if}
								{#if streamContent}
									<div class="turn-text streaming-text">
										{@html renderMessage(streamContent)}<span class="stream-cursor" aria-hidden="true"></span>
									</div>
								{:else if !streamReasoning}
									<div class="thinking"><span></span><span></span><span></span></div>
								{/if}
							</div>
						</article>
					{/if}
				{/if}
			</div>
		</div>

		{#if showScroll}
			<button type="button" class="scroll-bottom" on:click={scrollToBottom} aria-label="Прокрутить вниз">
				<ChevronDown size={15} strokeWidth={1.9} />
			</button>
		{/if}
		<div class="composer-wrap">
			<form class="composer" on:submit|preventDefault={() => send()}>
				<input
					bind:this={documentInput}
					class="file-input"
					type="file"
					accept=".docx,.xlsx,.xls,.pptx,.pdf,.txt,.md,.markdown,.csv,.tsv,.json,.xml,.yaml,.yml,.log,.html,.htm"
					on:change={handleDocumentFile}
				/>
				<input
					bind:this={imageInput}
					class="file-input"
					type="file"
					accept="image/png,image/jpeg,image/webp,image/gif"
					on:change={handleDocumentFile}
				/>
				{#if screenLabel !== 'ассистент' && !searchQuery}
					<span class="context-chip">из {screenLabel}</span>
				{/if}
				{#if attachedDocuments.length > 0}
					<div class="composer-attachments">
						{#each attachedDocuments as file (file.id)}
							<span class="attached-chip">
								<FileText size={11} strokeWidth={1.8} />
								{file.filename}
								<button type="button" aria-label={`Убрать ${file.filename}`} on:click={() => removeAttachedDocument(file.id)}>×</button>
							</span>
						{/each}
					</div>
				{/if}
				<textarea
					bind:this={textareaEl}
					bind:value={input}
					rows="1"
					placeholder="Напишите ассистенту: задачу, вопрос или просьбу о планах…"
					aria-label="Сообщение ассистенту"
					on:keydown={handleComposerKey}
				></textarea>
				<div class="composer-bar">
					<div class="composer-tools">
						<button type="button" class="tool" title="Прикрепить документ" aria-label="Прикрепить документ" on:click={openDocumentPicker}>
							<Paperclip size={15} strokeWidth={1.7} />
						</button>
						<button type="button" class="tool" title="Прикрепить изображение" aria-label="Прикрепить изображение" on:click={openImagePicker}>
							<ImageIcon size={15} strokeWidth={1.7} />
						</button>
						<button type="button" class="voice" disabled title="Голосовой ввод появится позже">
							<Mic size={13} strokeWidth={1.8} />
							<span>Голос</span>
						</button>
					</div>
					{#if sending}
						<button type="button" class="stop" on:click={stopGeneration}>
							<Square size={12} strokeWidth={2.2} aria-hidden="true" />
							Остановить
						</button>
					{:else}
						<button type="submit" class="send" disabled={!input.trim()}>
							Отправить
							<ArrowUp size={14} strokeWidth={2} aria-hidden="true" />
						</button>
					{/if}
				</div>
			</form>
			<p class="disclaimer">Ассистент может ошибаться — проверяйте важные даты и суммы</p>
		</div>
	</section>

	<aside class="today-rail" aria-label="План на сегодня">
		<header class="rail-top">
			<span class="sync-pill" class:is-offline={!online}>
				<span class="sync-dot" aria-hidden="true"></span>
				{online ? 'Все задачи синхронизированы' : 'Нет синхронизации'}
			</span>
			<button
				type="button"
				class="rail-refresh"
				aria-label="Обновить данные"
				title="Обновить данные"
				on:click={refreshAll}
			>
				<span class:spin={refreshing}><RefreshCw size={13} strokeWidth={1.8} /></span>
			</button>
		</header>

		<div class="rail-scroll">
			<section class="rail-card">
				<div class="rail-card-head">
					<h2>Сегодня</h2>
					<span class="count-badge">{doneToday}/{totalToday}</span>
				</div>
				<p class="rail-sub">
					{formatLongDate(new Date())} · {summary?.today.length ?? 0}
					{taskWord(summary?.today.length ?? 0)} с дедлайном
				</p>
				<div class="progress" aria-label={`Закрыто ${doneToday} из ${totalToday}`}>
					<span style={`width:${progress}%`}></span>
				</div>
				<p class="rail-caption">
					{doneToday} из {totalToday} закрыто ·
					{nextDeadline ? `следующий дедлайн в ${nextDeadline}` : 'без точного времени'}
				</p>

				{#if contextLoading}
					<div class="rail-skeleton">
						<span class="skeleton-line"></span>
						<span class="skeleton-line"></span>
						<span class="skeleton-line skeleton-line--short"></span>
					</div>
				{:else if contextError}
					<div class="rail-error">
						<span>{contextError}</span>
						<button type="button" on:click={refreshAll}>Повторить</button>
					</div>
				{:else if planItems.length === 0}
					<p class="rail-empty">На сегодня задач нет. Можно спланировать день в чате.</p>
				{:else}
					<ul class="plan-list">
						{#each planItems as item (item.id)}
							<li class:overdue={item.days_overdue !== undefined}>
								<button
									type="button"
									class="check"
									aria-label={`Завершить задачу «${item.title}»`}
									disabled={planBusyId === item.id}
									on:click={() => completePlan(item.id)}
								></button>
								<div class="plan-copy">
									<span class="plan-title">{item.title}</span>
									<span class="plan-meta">
										{item.days_overdue
											? `${item.days_overdue} дн. просрочено`
											: item.time !== '—'
												? `Сегодня в ${item.time}`
												: 'Без точного времени'}
										{#if item.priority === 'high'} · высокий приоритет{/if}
									</span>
								</div>
								<span class="plan-time">{item.time === '—' ? '' : item.time}</span>
							</li>
						{/each}
					</ul>
				{/if}

				<a class="rail-button" href="/today">
					<span>Весь план на день</span>
					<ChevronRight size={13} strokeWidth={1.8} />
				</a>
			</section>

			<section class="rail-card">
				<div class="rail-card-head">
					<h2 class="rail-kicker">Не закрытые задачи на {weekRange}</h2>
					<span class="count-badge">{weekTasks.length}</span>
				</div>

				<div class="week-days" aria-label="Дни недели">
					{#each weekDays as day}
						<a
							class="week-day"
							class:today={day.today}
							href={`/calendar?date=${day.key}`}
							title={`${day.date} · ${day.count} задач`}
						>
							<span>{day.label}</span>

						</a>
					{/each}
				</div>

				{#if weekRows.length === 0}
					<p class="rail-empty">На неделе нет открытых задач с датой.</p>
				{:else}
					<ul class="week-list">
						{#each weekRows as task (task.id)}
							{@const key = taskDate(task)}
							<li>
								<a href={key ? `/calendar?date=${key}` : '/tasks'}>
									<span class="week-date">{key ? formatShortDay(key) : '—'}</span>
									<span class="week-copy">
										<span class="week-title">{task.title}</span>
										<span class="week-meta">
											{key ? formatDate(key) : 'Без даты'}
											{#if task.due_at} · {formatTime(task.due_at)}{/if}
											· {priorityLabel(task.priority).toLowerCase()}
										</span>
									</span>
								</a>
							</li>
						{/each}
					</ul>
				{/if}

				<a class="rail-button" href="/tasks">
					<span>Показать все важные</span>
					<ChevronRight size={13} strokeWidth={1.8} />
				</a>
			</section>
		</div>
	</aside>
</div>

{#if documentOpen}
	<div class="doc-backdrop" role="presentation" on:click={closeDocument}>
		<div class="doc-dialog" role="dialog" aria-modal="true" aria-labelledby="doc-title" tabindex="-1" on:click|stopPropagation on:keydown|stopPropagation>
			<header class="doc-head">
				<div class="doc-head-copy">
					<span class="doc-kicker">Документ</span>
					<h2 id="doc-title">{document?.filename ?? documentUploadingName}</h2>
					{#if document}
						<p>{documentSize(document.size)} · {documentStatusLabel(document.status)}</p>
					{:else if documentUploading}
						<p>Загружаю файл…</p>
					{/if}
				</div>
				<button type="button" class="icon-close" aria-label="Закрыть" on:click={closeDocument}>×</button>
			</header>

			{#if documentError}
				<div class="doc-error" role="alert">{documentError}</div>
			{/if}

			{#if documentUploading}
				<div class="doc-processing">
					<span class="doc-spinner" aria-hidden="true"></span>
					<span>Загружаю «{documentUploadingName}»…</span>
				</div>
			{:else if document}
				{#if document.status === 'pending' || document.status === 'processing'}
					<div class="doc-processing">
						<span class="doc-spinner" aria-hidden="true"></span>
						<span>Конвертирую документ в Markdown…</span>
					</div>
				{/if}

				{#if document.status === 'failed'}
					<p class="doc-failed">{document.error ?? 'Не удалось обработать документ'}</p>
				{/if}

				{#if document.status === 'processed'}
					<p class="doc-ready">
						Документ сконвертирован в Markdown. Прикрепите его к сообщению — ассистент прочитает содержимое через инструмент.
					</p>
				{/if}

				<footer class="doc-actions">
					{#if document.status === 'failed'}
						<button type="button" class="btn-primary" on:click={retryDocumentProcessing}>
							Повторить обработку
						</button>
					{/if}
					{#if document.status === 'processed'}
						<button type="button" class="btn-primary" on:click={attachDocument}>
							Прикрепить к сообщению
						</button>
					{/if}
					<button type="button" class="btn-ghost" on:click={closeDocument}>Закрыть</button>
				</footer>
			{:else}
				<footer class="doc-actions">
					<button type="button" class="btn-ghost" on:click={closeDocument}>Закрыть</button>
				</footer>
			{/if}
		</div>
	</div>
{/if}

<style>
	.assistant-page {
		display: grid;
		grid-template-columns: minmax(0, 1fr) var(--rail-w);
		height: 100dvh;
		background: var(--bg-primary);
		overflow: hidden;
		/* Width of the reading column. Large displays get a wider column
		   because a 776px transcript wastes most of a 1920px+ screen. */
		--chat-measure: 776px;
	}

	@media (min-width: 1360px) {
		.assistant-page {
			--chat-measure: 856px;
		}
	}

	@media (min-width: 1600px) {
		.assistant-page {
			--chat-measure: 920px;
		}
	}

	@media (min-width: 1920px) {
		.assistant-page {
			--chat-measure: 1000px;
		}
	}

	@media (min-width: 2560px) {
		.assistant-page {
			--chat-measure: 1120px;
		}
	}

	.chat-column {
		display: flex;
		min-width: 0;
		height: 100dvh;
		flex-direction: column;
		background: var(--bg-primary);
	}

	.chat-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-4);
		height: var(--topbar-h);
		min-height: var(--topbar-h);
		padding: 0 20px;
		border-bottom: 1px solid var(--border-soft);
	}

	.chat-title {
		min-width: 0;
	}

	.chat-title h1 {
		margin: 0;
		font-size: 0.9375rem;
		font-weight: 600;
		letter-spacing: -0.01em;
	}

	.sync-line {
		display: flex;
		align-items: center;
		gap: 5px;
		margin: 2px 0 0;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.sync-dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--success);
		box-shadow: 0 0 0 3px var(--success-surface);
		flex-shrink: 0;
	}

	.sync-line.is-offline,
	.sync-pill.is-offline {
		color: var(--danger-text);
	}

	.sync-line.is-offline .sync-dot,
	.sync-pill.is-offline .sync-dot {
		background: var(--danger);
		box-shadow: 0 0 0 3px var(--danger-surface);
	}

	.chat-tools {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
	}

	.search {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 154px;
		height: 28px;
		padding: 0 8px;
		border: 1px solid var(--border);
		border-radius: 7px;
		background: var(--bg-panel);
		color: var(--fg-faint);
	}

	.search:focus-within {
		border-color: var(--accent-border);
		box-shadow: 0 0 0 2px var(--accent-surface);
	}

	.search input {
		width: 100%;
		min-width: 0;
		border: none;
		background: transparent;
		padding: 0;
		font-size: var(--text-xs);
		color: var(--fg-body);
		outline: none;
	}

	.search input:focus {
		box-shadow: none;
	}

	.search input::placeholder {
		color: var(--fg-faint);
	}

	.menu {
		position: relative;
	}

	.menu summary {
		display: grid;
		place-items: center;
		width: 28px;
		height: 28px;
		border: 1px solid var(--border);
		border-radius: 7px;
		color: var(--fg-muted);
		cursor: pointer;
		list-style: none;
		font-size: 13px;
		letter-spacing: 1px;
		line-height: 1;
	}

	.menu summary::-webkit-details-marker {
		display: none;
	}

	.menu[open] summary,
	.menu summary:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.menu-pop {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		z-index: 20;
		display: flex;
		width: 168px;
		flex-direction: column;
		padding: 4px;
		border: 1px solid var(--border);
		border-radius: 9px;
		background: var(--bg-raised);
		box-shadow: var(--shadow-overlay);
	}

	.menu-pop button {
		padding: 7px 9px;
		border: none;
		border-radius: 6px;
		background: transparent;
		color: var(--fg-body);
		font-size: var(--text-sm);
		text-align: left;
	}

	.menu-pop button:hover {
		background: var(--bg-hover);
		color: var(--fg-primary);
	}

	.error-banner {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		margin: 10px 20px 0;
		padding: 8px 10px;
		border: 1px solid var(--danger-border);
		border-radius: 8px;
		background: var(--danger-surface);
		color: var(--danger-text);
		font-size: var(--text-sm);
		flex-shrink: 0;
	}

	.error-actions {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-shrink: 0;
	}

	.text-button {
		padding: 4px 8px;
		border: none;
		border-radius: 5px;
		background: transparent;
		color: var(--danger-text);
		font-size: var(--text-xs);
		font-weight: 550;
	}

	.text-button:hover {
		background: var(--danger-surface);
	}

	.icon-close {
		display: grid;
		place-items: center;
		width: 24px;
		height: 24px;
		border: none;
		background: transparent;
		color: inherit;
		font-size: 16px;
	}

	.search-note {
		padding: 7px 20px 0;
		color: var(--fg-faint);
		font-size: var(--text-xs);
		flex-shrink: 0;
	}

	.chat-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		scrollbar-width: none;
	}

	.chat-scroll::-webkit-scrollbar {
		width: 0;
		height: 0;
	}

	.chat-inner {
		max-width: var(--chat-measure);
		margin: 0 auto;
		padding: 22px 20px 18px;
	}

	.day-divider {
		display: flex;
		justify-content: center;
		margin: 8px 0 26px;
	}

	.day-divider span {
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.turn {
		display: grid;
		grid-template-columns: 36px minmax(0, 1fr);
		gap: 12px;
		margin-bottom: 26px;
		animation: turn-in var(--duration-normal) var(--ease-out);
	}

	.avatar {
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		border-radius: 10px;
		flex-shrink: 0;
	}

	.avatar--assistant {
		background: linear-gradient(145deg, #8b6dff, #5b3fd6);
		color: #fff;
		box-shadow: 0 0 0 1px rgba(154, 131, 255, 0.28) inset;
	}

	.avatar--user {
		border-radius: 50%;
		background: var(--bg-raised-2);
		border: 1px solid var(--border);
		color: var(--fg-muted);
		font-size: 11px;
		font-weight: 600;
	}

	.turn-main {
		min-width: 0;
	}

	.turn-meta {
		display: flex;
		align-items: baseline;
		gap: 8px;
		margin-bottom: 6px;
	}

	.author {
		color: var(--fg-primary);
		font-size: var(--text-xs);
		font-weight: 600;
	}

	.turn-meta time {
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		font-variant-numeric: tabular-nums;
	}

	.turn-text {
		color: var(--fg-body);
		font-size: var(--text-base);
		line-height: 1.62;
		overflow-wrap: anywhere;
	}

	.turn-text :global(strong) {
		color: var(--fg-primary);
		font-weight: 600;
	}

	.turn-text :global(code) {
		padding: 1px 4px;
		border-radius: 4px;
		background: var(--bg-raised-2);
		font-family: var(--font-mono);
		font-size: 0.92em;
	}

	.turn--user {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 5px;
	}

	.user-meta {
		display: flex;
		gap: 7px;
		padding-right: 46px;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.user-meta span {
		color: var(--fg-muted);
		font-weight: 550;
	}

	.user-line {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		max-width: 88%;
	}

	.user-bubble {
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: 10px;
		background: var(--bg-raised-2);
		color: var(--fg-body);
		font-size: var(--text-base);
		line-height: 1.55;
		overflow-wrap: anywhere;
	}

	.task-card {
		display: flex;
		align-items: center;
		gap: 11px;
		width: 100%;
		margin-top: 12px;
		padding: 11px 12px;
		border: 1px solid var(--border);
		border-radius: 9px;
		background: var(--ink-800);
	}

	.task-card + .task-card {
		margin-top: 6px;
	}

	.task-card--done {
		opacity: 0.62;
	}

	.task-icon {
		display: grid;
		place-items: center;
		width: 32px;
		height: 32px;
		border-radius: 8px;
		background: var(--accent-surface);
		color: var(--accent-bright);
		flex-shrink: 0;
	}

	.task-copy {
		display: flex;
		min-width: 0;
		flex: 1;
		flex-direction: column;
		gap: 3px;
	}

	.task-title {
		color: var(--fg-primary);
		font-size: var(--text-base);
		font-weight: 550;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.task-meta {
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.task-badge {
		padding: 3px 7px;
		border-radius: 5px;
		font-size: var(--text-2xs);
		font-weight: 550;
		white-space: nowrap;
		flex-shrink: 0;
	}

	.task-badge[data-tone='success'] {
		background: var(--success-surface);
		color: #86efac;
	}

	.task-badge[data-tone='accent'] {
		background: var(--accent-surface);
		color: var(--accent-bright);
	}

	.action-card {
		margin-top: 12px;
		padding: 12px;
		border: 1px solid var(--accent-border);
		border-radius: 9px;
		background: var(--accent-surface);
	}

	.action-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}

	.action-label {
		color: var(--accent-bright);
		font-size: var(--text-2xs);
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.action-head code {
		color: var(--fg-muted);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
	}

	.action-copy {
		margin: 7px 0 10px;
		color: var(--fg-body);
		font-size: var(--text-sm);
		line-height: 1.5;
	}

	.action-buttons {
		display: flex;
		gap: 6px;
	}

	.btn-primary,
	.btn-ghost {
		height: 30px;
		padding: 0 11px;
		border-radius: 6px;
		font-size: var(--text-xs);
		font-weight: 550;
	}

	.btn-primary {
		border: 1px solid var(--accent);
		background: var(--accent);
		color: #fff;
	}

	.btn-primary:hover:not(:disabled) {
		background: var(--accent-bright);
		border-color: var(--accent-bright);
	}

	.btn-ghost {
		border: 1px solid var(--border);
		background: transparent;
		color: var(--fg-muted);
	}

	.btn-ghost:hover:not(:disabled) {
		color: var(--fg-primary);
		border-color: var(--ink-700);
	}

	.btn-primary:disabled,
	.btn-ghost:disabled {
		opacity: 0.5;
	}

	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
		margin-top: 12px;
	}

	.chip {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		height: 27px;
		padding: 0 9px;
		border: 1px solid var(--ink-700);
		border-radius: 6px;
		background: var(--bg-raised);
		color: var(--fg-body);
		font-size: var(--text-xs);
	}

	.chip:hover {
		border-color: var(--ink-700);
		background: var(--bg-panel);
		color: var(--fg-body);
	}

	.thinking {
		display: flex;
		align-items: center;
		gap: 4px;
		height: 36px;
	}

	.thinking span {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--fg-muted);
		animation: blink 1.2s infinite ease-in-out;
	}

	.thinking span:nth-child(2) {
		animation-delay: 0.15s;
	}

	.thinking span:nth-child(3) {
		animation-delay: 0.3s;
	}

	.empty-state {
		max-width: 520px;
		margin: 12vh auto 0;
		text-align: left;
	}

	.empty-mark {
		display: grid;
		place-items: center;
		width: 38px;
		height: 38px;
		margin-bottom: 14px;
		border-radius: 10px;
		background: var(--accent-surface);
		color: var(--accent-bright);
	}

	.empty-state h2 {
		margin: 0;
		font-size: var(--text-3xl);
		font-weight: 650;
		letter-spacing: -0.02em;
	}

	.empty-state p {
		margin: 8px 0 18px;
		color: var(--fg-muted);
		font-size: var(--text-base);
		line-height: 1.6;
	}

	.suggestions {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
	}

	.suggestion {
		height: 30px;
		padding: 0 10px;
		border: 1px solid var(--border);
		border-radius: 7px;
		background: var(--bg-panel);
		color: var(--fg-body);
		font-size: var(--text-sm);
	}

	.suggestion:hover {
		border-color: var(--accent-border);
		background: var(--accent-surface);
	}

	.loading-state {
		display: flex;
		flex-direction: column;
		gap: 28px;
		padding-top: 14px;
	}

	.skeleton-turn {
		display: flex;
		flex-direction: column;
		gap: 10px;
	}

	.skeleton-turn--offset {
		margin-left: 48px;
	}

	.skeleton-line {
		display: block;
		width: 100%;
		height: 11px;
		border-radius: var(--radius-full);
		background: linear-gradient(90deg, var(--bg-raised), var(--bg-hover), var(--bg-raised));
		background-size: 200% 100%;
		animation: shimmer 1.4s infinite linear;
	}

	.skeleton-line--short {
		width: 26%;
	}

	.skeleton-line--medium {
		width: 62%;
	}

	.composer-wrap {
		flex-shrink: 0;
		padding: 0 20px 12px;
	}

	.composer {
		position: relative;
		width: 100%;
		max-width: var(--chat-measure);
		margin: 0 auto;
		padding: 9px 9px 8px;
		border: 1px solid var(--border);
		border-radius: 11px;
		background: var(--bg-panel);
		transition:
			border-color var(--duration-normal) var(--ease-out),
			box-shadow var(--duration-normal) var(--ease-out);
	}

	.composer:focus-within {
		border-color: rgba(124, 92, 252, 0.5);
		box-shadow: 0 0 0 2px var(--accent-surface);
	}

	.context-chip {
		display: inline-block;
		margin: 0 0 5px 3px;
		padding: 2px 6px;
		border-radius: 4px;
		background: var(--accent-surface);
		color: var(--accent-bright);
		font-size: var(--text-2xs);
	}

	.composer textarea {
		display: block;
		width: 100%;
		min-height: 34px;
		max-height: 140px;
		padding: 4px 4px 6px;
		border: none;
		background: transparent;
		color: var(--fg-primary);
		font-size: var(--text-base);
		line-height: 1.5;
		resize: none;
		outline: none;
	}

	.composer textarea:focus {
		box-shadow: none;
	}

	.composer textarea::placeholder {
		color: var(--fg-faint);
	}

	.composer-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
	}

	.composer-tools {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.tool {
		display: grid;
		place-items: center;
		width: 28px;
		height: 28px;
		border: none;
		border-radius: 6px;
		background: transparent;
		color: var(--fg-faint);
	}

	.tool:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	.voice {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		height: 26px;
		padding: 0 8px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: transparent;
		color: var(--fg-muted);
		font-size: var(--text-2xs);
	}

	.voice:disabled {
		opacity: 0.65;
		cursor: not-allowed;
	}

	.send {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		height: 32px;
		padding: 0 11px;
		border: 1px solid var(--accent);
		border-radius: 7px;
		background: var(--accent);
		color: #fff;
		font-size: var(--text-xs);
		font-weight: 550;
	}

	.send:hover:not(:disabled) {
		background: var(--accent-bright);
		border-color: var(--accent-bright);
	}

	.send:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.disclaimer {
		margin: 7px 0 0;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		text-align: center;
	}

	.today-rail {
		display: flex;
		min-width: 0;
		height: 100dvh;
		flex-direction: column;
		border-left: 1px solid var(--border-soft);
		background: var(--bg-primary);
	}

	.rail-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		height: var(--topbar-h);
		min-height: var(--topbar-h);
		padding: 0 14px;
	}

	.sync-pill {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 4px 8px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg-panel);
		color: var(--fg-muted);
		font-size: var(--text-2xs);
		white-space: nowrap;
	}

	.rail-refresh {
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border: none;
		border-radius: 6px;
		background: transparent;
		color: var(--fg-muted);
		flex-shrink: 0;
	}

	.rail-refresh:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.spin {
		animation: spin 0.9s linear infinite;
	}

	.rail-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 4px 14px 16px;
		display: flex;
		flex-direction: column;
		gap: 12px;
		scrollbar-width: none;
	}

	.rail-scroll::-webkit-scrollbar {
		width: 0;
		height: 0;
	}

	.rail-card {
		padding: 13px;
		border: 1px solid var(--border);
		border-radius: 10px;
		background: var(--ink-850);
	}

	.rail-card-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
	}

	.rail-card-head h2 {
		margin: 0;
		font-size: var(--text-base);
		font-weight: 600;
		letter-spacing: -0.01em;
	}

	.rail-card-head h2.rail-kicker {
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		font-weight: 500;
		letter-spacing: 0;
	}

	.count-badge {
		padding: 2px 6px;
		border-radius: 5px;
		background: var(--bg-raised);
		color: var(--fg-muted);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		font-variant-numeric: tabular-nums;
	}

	.rail-sub {
		margin: 5px 0 9px;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.progress {
		height: 3px;
		overflow: hidden;
		border-radius: 2px;
		background: var(--bg-raised);
	}

	.progress span {
		display: block;
		height: 100%;
		border-radius: inherit;
		background: var(--accent);
		transition: width var(--duration-normal) var(--ease-out);
	}

	.rail-caption {
		margin: 6px 0 10px;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.plan-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin: 0 -4px;
		padding: 0;
		list-style: none;
	}

	.plan-list li {
		display: flex;
		align-items: flex-start;
		gap: 8px;
		padding: 5px 4px;
		border-radius: 6px;
	}

	.plan-list li:hover {
		background: var(--bg-raised);
	}

	.check {
		width: 15px;
		height: 15px;
		margin-top: 1px;
		padding: 0;
		border: 1.4px solid var(--ink-700);
		border-radius: 4px;
		background: transparent;
		flex-shrink: 0;
		position: relative;
	}

	.check:hover:not(:disabled) {
		border-color: var(--accent);
	}

	.check:disabled {
		opacity: 0.5;
	}

	.plan-copy {
		display: flex;
		min-width: 0;
		flex: 1;
		flex-direction: column;
		gap: 2px;
	}

	.plan-title {
		color: var(--fg-body);
		font-size: var(--text-xs);
		line-height: 1.35;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.plan-meta {
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		line-height: 1.35;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.plan-list li.overdue .plan-meta {
		color: var(--danger);
	}

	.plan-time {
		color: var(--fg-muted);
		font-size: var(--text-2xs);
		font-variant-numeric: tabular-nums;
		flex-shrink: 0;
	}

	.rail-button {
		display: flex;
		align-items: center;
		justify-content: space-between;
		height: 30px;
		margin-top: 9px;
		padding: 0 9px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg-raised);
		color: var(--fg-body);
		font-size: var(--text-xs);
		text-decoration: none;
	}

	.rail-button:hover {
		border-color: var(--ink-700);
		color: var(--fg-primary);
		text-decoration: none;
	}

	.week-days {
		display: grid;
		grid-template-columns: repeat(7, minmax(0, 1fr));
		gap: 3px;
		margin: 9px 0 10px;
	}

	.week-day {
		display: flex;
		height: 25px;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--bg-raised);
		color: var(--fg-muted);
		font-size: 9px;
		text-decoration: none;
		line-height: 1;
	}

	.week-day:hover {
		border-color: var(--accent-border);
		color: var(--fg-body);
		text-decoration: none;
	}

	.week-day.today {
		border-color: var(--accent-border);
		background: var(--accent-surface);
		color: var(--accent-bright);
	}

	.week-list {
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.week-list a {
		display: flex;
		align-items: flex-start;
		gap: 8px;
		padding: 6px 0;
		border-top: 1px solid var(--border-soft);
		text-decoration: none;
	}

	.week-list a:hover {
		text-decoration: none;
	}

	.week-list a:hover .week-title {
		color: var(--accent-bright);
	}

	.week-date {
		display: grid;
		place-items: center;
		width: 24px;
		height: 20px;
		margin-top: 1px;
		border: 1px solid var(--border);
		border-radius: 4px;
		background: var(--bg-raised);
		color: var(--fg-muted);
		font-size: 8.5px;
		flex-shrink: 0;
	}

	.week-copy {
		display: flex;
		min-width: 0;
		flex-direction: column;
		gap: 1px;
	}

	.week-title {
		color: var(--fg-body);
		font-size: var(--text-xs);
		line-height: 1.35;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.week-meta {
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.rail-empty {
		margin: 8px 0;
		color: var(--fg-faint);
		font-size: var(--text-xs);
		line-height: 1.5;
	}

	.rail-skeleton {
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding: 4px 0 8px;
	}

	.rail-error {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 4px 0 8px;
		color: var(--danger-text);
		font-size: var(--text-xs);
	}

	.rail-error button {
		align-self: flex-start;
		height: 26px;
		padding: 0 8px;
		border: 1px solid var(--danger-border);
		border-radius: 5px;
		background: transparent;
		color: var(--danger-text);
		font-size: var(--text-xs);
	}

	@keyframes turn-in {
		from {
			opacity: 0;
			transform: translateY(4px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}

	@keyframes pop-in {
		from {
			opacity: 0;
			transform: translateY(-4px) scale(0.98);
		}
		to {
			opacity: 1;
			transform: translateY(0) scale(1);
		}
	}

	@keyframes card-in {
		from {
			opacity: 0;
			transform: translateY(6px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}

	@keyframes shimmer {
		from {
			background-position: 200% 0;
		}
		to {
			background-position: -200% 0;
		}
	}

	@keyframes blink {
		0%,
		80%,
		100% {
			opacity: 0.25;
			transform: translateY(0);
		}
		40% {
			opacity: 1;
			transform: translateY(-2px);
		}
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	.file-input {
		display: none;
	}

	.doc-backdrop {
		position: fixed;
		inset: 0;
		z-index: 60;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 24px;
		background: rgba(3, 3, 6, 0.72);
		backdrop-filter: blur(4px);
	}

	.doc-dialog {
		display: flex;
		width: min(620px, 100%);
		max-height: min(82dvh, 760px);
		flex-direction: column;
		padding: 18px;
		border: 1px solid var(--border);
		border-radius: 14px;
		background: var(--bg-card);
		box-shadow: var(--shadow-overlay);
		outline: none;
	}

	.doc-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 12px;
		padding-bottom: 12px;
		border-bottom: 1px solid var(--border-soft);
	}

	.doc-head-copy {
		min-width: 0;
	}

	.doc-kicker {
		color: var(--accent-bright);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		letter-spacing: 0.08em;
		text-transform: uppercase;
	}

	.doc-head h2 {
		margin: 3px 0 0;
		font-size: var(--text-md);
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.doc-head p {
		margin: 3px 0 0;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.doc-dialog .icon-close {
		width: 28px;
		height: 28px;
		border-radius: 6px;
		color: var(--fg-muted);
	}

	.doc-dialog .icon-close:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.doc-error,
	.doc-failed {
		margin: 12px 0 0;
		padding: 8px 10px;
		border: 1px solid var(--danger-border);
		border-radius: 7px;
		background: var(--danger-surface);
		color: var(--danger-text);
		font-size: var(--text-xs);
		line-height: 1.45;
	}

	.doc-processing {
		display: flex;
		align-items: center;
		gap: 9px;
		padding: 22px 4px;
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	.doc-spinner {
		width: 14px;
		height: 14px;
		border: 2px solid var(--border);
		border-top-color: var(--accent);
		border-radius: 50%;
		animation: spin 0.9s linear infinite;
		flex-shrink: 0;
	}

	.doc-ready {
		margin: 14px 0 0;
		padding: 10px 12px;
		border: 1px solid var(--success-surface);
		border-radius: 7px;
		background: rgba(52, 211, 153, 0.05);
		color: var(--fg-muted);
		font-size: var(--text-sm);
		line-height: 1.5;
	}

	.user-docs {
		display: flex;
		flex-wrap: wrap;
		justify-content: flex-end;
		gap: 5px;
		max-width: 88%;
		padding-right: 46px;
	}

	.doc-chip {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		max-width: 260px;
		padding: 3px 7px;
		border: 1px solid var(--accent-border);
		border-radius: 5px;
		background: var(--accent-surface);
		color: var(--accent-bright);
		font-size: var(--text-2xs);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.composer-attachments {
		display: flex;
		flex-wrap: wrap;
		gap: 5px;
		margin: 0 0 7px 3px;
	}

	.attached-chip {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		max-width: 100%;
		padding: 3px 4px 3px 7px;
		border: 1px solid var(--accent-border);
		border-radius: 5px;
		background: var(--accent-surface);
		color: var(--accent-bright);
		font-size: var(--text-2xs);
	}

	.attached-chip button {
		display: grid;
		place-items: center;
		width: 16px;
		height: 16px;
		padding: 0;
		border: none;
		border-radius: 3px;
		background: transparent;
		color: inherit;
		font-size: 13px;
		line-height: 1;
	}

	.attached-chip button:hover {
		background: rgba(124, 92, 252, 0.2);
	}

	.doc-actions {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 7px;
		margin-top: 14px;
		padding-top: 12px;
		border-top: 1px solid var(--border-soft);
	}

	.model-picker {
		position: relative;
		display: inline-flex;
		align-items: center;
	}

	.model-trigger {
		display: inline-flex;
		max-width: 172px;
		height: 28px;
		align-items: center;
		gap: 5px;
		padding: 0 7px;
		border: 1px solid var(--border);
		border-radius: 7px;
		background: var(--bg-panel);
		color: var(--fg-muted);
		font-size: var(--text-2xs);
		transition:
			background-color var(--duration-fast) var(--ease-out),
			border-color var(--duration-fast) var(--ease-out),
			color var(--duration-fast) var(--ease-out);
	}

	.model-trigger:hover {
		border-color: var(--accent-border);
		background: var(--bg-raised);
		color: var(--fg-body);
	}

	.model-picker.open .model-trigger {
		border-color: var(--accent-border);
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.model-name {
		min-width: 0;
		max-width: 128px;
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
	}

	.model-chevron {
		display: inline-flex;
		flex-shrink: 0;
		transition: transform var(--duration-normal) var(--ease-out);
	}

	.model-picker.open .model-chevron {
		transform: rotate(180deg);
	}

	.model-pop {
		position: absolute;
		top: calc(100% + 6px);
		right: 0;
		z-index: 30;
		display: flex;
		width: min(340px, calc(100vw - 32px));
		max-height: 60vh;
		flex-direction: column;
		overflow-y: auto;
		padding: 5px;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
		box-shadow: var(--shadow-overlay);
		transform-origin: top right;
		animation: pop-in var(--duration-normal) var(--ease-out);
		overscroll-behavior: contain;
	}

	.model-pop-label {
		margin: 0;
		padding: 5px 8px 7px;
		border-bottom: 1px solid var(--border-soft);
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
	}

	.model-empty {
		margin: 0;
		padding: 10px 8px;
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	.model-option {
		display: flex;
		width: 100%;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 7px 8px;
		border: none;
		border-radius: 6px;
		background: transparent;
		color: var(--fg-body);
		font-size: var(--text-xs);
		text-align: left;
		transition:
			background-color var(--duration-fast) var(--ease-out),
			color var(--duration-fast) var(--ease-out);
	}

	.model-option:hover,
	.model-option:focus-visible {
		background: var(--bg-hover);
		color: var(--fg-primary);
	}

	.model-option.selected {
		background: var(--accent-surface);
		color: var(--violet-bright);
	}

	.model-option-name {
		min-width: 0;
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
	}

	.menu-toggle {
		display: flex;
		align-items: center;
		gap: 7px;
		margin-top: 3px;
		padding: 7px 9px;
		border-top: 1px solid var(--border-soft);
		color: var(--fg-body);
		font-size: var(--text-xs);
		cursor: pointer;
	}

	.menu-toggle input {
		width: 14px;
		height: 14px;
		margin: 0;
		accent-color: var(--accent);
	}

	.menu-hint {
		margin: 0;
		padding: 0 9px 4px;
		color: var(--fg-faint);
		font-size: 9px;
		line-height: 1.4;
	}

	.copy-button {
		display: grid;
		place-items: center;
		width: 22px;
		height: 22px;
		margin-left: auto;
		border: none;
		border-radius: 5px;
		background: transparent;
		color: var(--fg-faint);
		opacity: 0.65;
	}

	.copy-button:hover,
	.copy-button:focus-visible {
		background: var(--bg-raised);
		color: var(--fg-body);
		opacity: 1;
	}

	.reasoning {
		margin: 0 0 9px;
		padding: 7px 9px;
		border: 1px solid var(--border-soft);
		border-radius: 7px;
		background: rgba(124, 92, 252, 0.035);
	}

	.reasoning summary {
		display: flex;
		align-items: center;
		gap: 5px;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		cursor: pointer;
		list-style: none;
	}

	.reasoning summary::-webkit-details-marker {
		display: none;
	}

	.reasoning[open] summary {
		color: var(--accent-bright);
		margin-bottom: 6px;
	}

	.reasoning-body {
		max-height: 190px;
		overflow-y: auto;
		color: var(--fg-muted);
		font-size: var(--text-xs);
		line-height: 1.55;
		white-space: normal;
		scrollbar-width: thin;
	}

	.reasoning-body :global(p) {
		margin: 0 0 6px;
	}

	.reasoning-body :global(p:last-child) {
		margin-bottom: 0;
	}

	.tool-activity {
		display: flex;
		flex-direction: column;
		gap: 4px;
		margin: 6px 0 9px;
	}

	.tool-row {
		display: flex;
		align-items: center;
		gap: 7px;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.tool-row[data-status='done'] {
		color: var(--fg-muted);
	}

	.tool-spinner {
		width: 11px;
		height: 11px;
		border: 1.5px solid var(--border);
		border-top-color: var(--accent);
		border-radius: 50%;
		animation: spin 0.8s linear infinite;
		flex-shrink: 0;
	}

	.streaming-label {
		color: var(--accent-bright);
		font-size: var(--text-2xs);
	}

	.stream-cursor {
		display: inline-block;
		width: 2px;
		height: 14px;
		margin-left: 2px;
		vertical-align: -2px;
		border-radius: 1px;
		background: var(--accent-bright);
		animation: cursor-blink 1s steps(2, start) infinite;
	}

	.scroll-bottom {
		position: absolute;
		right: 22px;
		bottom: 188px;
		z-index: 10;
		display: grid;
		place-items: center;
		width: 30px;
		height: 30px;
		border: 1px solid var(--border);
		border-radius: 50%;
		background: var(--bg-raised);
		color: var(--fg-muted);
		box-shadow: 0 6px 20px rgba(0, 0, 0, 0.35);
	}

	.scroll-bottom:hover {
		color: var(--fg-primary);
		border-color: var(--ink-700);
	}

	.stop {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		height: 32px;
		padding: 0 11px;
		border: 1px solid var(--danger-border);
		border-radius: 7px;
		background: var(--danger-surface);
		color: var(--danger-text);
		font-size: var(--text-xs);
		font-weight: 550;
	}

	.stop:hover {
		background: rgba(248, 113, 113, 0.15);
	}

	.turn-text,
	.user-bubble {
		white-space: normal;
	}

	.turn-text :global(p),
	.user-bubble :global(p) {
		margin: 0 0 9px;
	}

	.turn-text :global(p:last-child),
	.user-bubble :global(p:last-child) {
		margin-bottom: 0;
	}

	.turn-text :global(h1),
	.turn-text :global(h2),
	.turn-text :global(h3),
	.turn-text :global(h4) {
		margin: 14px 0 6px;
		color: var(--fg-primary);
		font-weight: 600;
		line-height: 1.3;
	}

	.turn-text :global(h1) {
		font-size: var(--text-xl);
	}

	.turn-text :global(h2) {
		font-size: var(--text-lg);
	}

	.turn-text :global(h3),
	.turn-text :global(h4) {
		font-size: var(--text-md);
	}

	.turn-text :global(ul),
	.turn-text :global(ol),
	.user-bubble :global(ul),
	.user-bubble :global(ol) {
		margin: 6px 0 10px;
		padding-left: 20px;
	}

	.turn-text :global(li),
	.user-bubble :global(li) {
		margin: 2px 0;
	}

	.turn-text :global(pre) {
		margin: 9px 0;
		padding: 10px 12px;
		overflow-x: auto;
		border: 1px solid var(--border);
		border-radius: 8px;
		background: #09090d;
		font-size: 11.5px;
		line-height: 1.5;
	}

	.turn-text :global(pre code) {
		padding: 0;
		background: none;
		font-size: inherit;
	}

	.turn-text :global(blockquote) {
		margin: 9px 0;
		padding: 2px 0 2px 11px;
		border-left: 2px solid var(--accent-border);
		color: var(--fg-muted);
	}

	.turn-text :global(a),
	.user-bubble :global(a) {
		color: var(--accent-bright);
		text-decoration: none;
	}

	.turn-text :global(a:hover),
	.user-bubble :global(a:hover) {
		text-decoration: underline;
	}

	.turn-text :global(hr) {
		margin: 14px 0;
		border: none;
		border-top: 1px solid var(--border);
	}

	.turn-text :global(.md-table) {
		margin: 10px 0;
		overflow-x: auto;
	}

	.turn-text :global(.md-table table) {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}

	.turn-text :global(.md-table th),
	.turn-text :global(.md-table td) {
		padding: 6px 8px;
		border: 1px solid var(--border);
		text-align: left;
	}

	.turn-text :global(.md-table th) {
		background: var(--bg-raised);
		color: var(--fg-primary);
		font-weight: 600;
	}

	.streaming-text {
		margin-top: 2px;
	}

	.turn--stream {
		margin-bottom: 20px;
	}

	/* Motion: interactive surfaces ease between states instead of snapping. */
	.suggestion,
	.chip,
	.btn-primary,
	.btn-ghost,
	.text-button,
	.icon-close,
	.copy-button,
	.tool,
	.voice,
	.scroll-bottom,
	.doc-chip,
	.search,
	.menu summary,
	.menu-pop button,
	.task-card,
	.action-card {
		transition:
			background-color var(--duration-fast) var(--ease-out),
			border-color var(--duration-fast) var(--ease-out),
			color var(--duration-fast) var(--ease-out),
			opacity var(--duration-fast) var(--ease-out),
			transform var(--duration-fast) var(--ease-out),
			box-shadow var(--duration-normal) var(--ease-out);
	}

	.btn-primary:active:not(:disabled),
	.btn-ghost:active:not(:disabled),
	.suggestion:active,
	.chip:active,
	.tool:active:not(:disabled),
	.scroll-bottom:active {
		transform: translateY(1px);
	}

	/* Cards and tool activity arrive with the message instead of popping in. */
	.task-card,
	.action-card,
	.tool-activity,
	.reasoning {
		animation: card-in var(--duration-normal) var(--ease-out) both;
	}

	.turn--stream .avatar--assistant {
		animation: breathe 2.4s var(--ease-out) infinite;
	}

	.provider-notice {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin: 0 0 8px;
		padding: 4px 9px;
		border: 1px solid var(--warning-border);
		border-radius: var(--radius-full);
		background: var(--warning-surface);
		color: var(--warning);
		font-size: var(--text-2xs);
		animation: card-in var(--duration-normal) var(--ease-out) both;
	}

	@keyframes breathe {
		0%,
		100% {
			box-shadow: 0 0 0 1px rgba(154, 131, 255, 0.28) inset;
		}
		50% {
			box-shadow:
				0 0 0 1px rgba(154, 131, 255, 0.5) inset,
				0 0 0 4px rgba(124, 92, 252, 0.12);
		}
	}

	@keyframes cursor-blink {
		50% {
			opacity: 0;
		}
	}

	@media (max-width: 1100px) {
		.assistant-page {
			grid-template-columns: minmax(0, 1fr);
			height: auto;
			overflow: visible;
		}

		.chat-column {
			height: 100dvh;
		}

		.today-rail {
			height: auto;
			border-left: none;
			border-top: 1px solid var(--border-soft);
		}

		.rail-top {
			padding-inline: 20px;
		}

		.rail-scroll {
			display: grid;
			grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
			gap: 12px;
			padding: 4px 20px 24px;
			overflow: visible;
		}
	}

	@media (max-width: 820px) {
		.model-trigger {
			max-width: 136px;
		}

		.model-name {
			max-width: 92px;
		}

		.chat-column {
			height: calc(100dvh - var(--mobile-nav-h));
		}

		.chat-header {
			padding-inline: 16px;
		}

		.chat-inner {
			padding: 18px 16px 12px;
		}

		.composer-wrap {
			padding: 0 12px 8px;
		}

		.rail-scroll {
			grid-template-columns: minmax(0, 1fr);
			padding: 4px 12px 16px;
		}

		.rail-top {
			padding-inline: 12px;
		}

		.error-banner {
			margin-inline: 16px;
		}

		.search-note {
			padding-inline: 16px;
		}

		.turn {
			grid-template-columns: 30px minmax(0, 1fr);
			gap: 9px;
		}

		.avatar {
			width: 30px;
			height: 30px;
			border-radius: 8px;
		}

		.avatar--user {
			width: 28px;
			height: 28px;
			font-size: 10px;
		}

		.user-line {
			max-width: 94%;
		}

		.turn-text {
			font-size: var(--text-base);
		}

		.turn--user {
			padding-right: 2px;
		}

		.user-meta {
			padding-right: 38px;
		}

		.empty-state {
			margin-top: 8vh;
		}

		.composer-tools .tool:nth-child(2) {
			display: none;
		}
	}

	@media (max-width: 560px) {
		.doc-backdrop {
			padding: 10px;
		}

		.doc-dialog {
			max-height: 88dvh;
			padding: 14px;
		}

		.doc-actions {
			flex-wrap: wrap;
		}

		.search {
			display: none;
		}

		.model-trigger {
			max-width: 120px;
		}

		.model-name {
			max-width: 78px;
		}

		.scroll-bottom {
			bottom: 158px;
			right: 14px;
		}

		.chat-header {
			gap: 8px;
		}

		.sync-line {
			max-width: 210px;
		}

		.day-divider {
			margin-bottom: 20px;
		}

		.task-card {
			padding: 10px;
		}

		.task-title {
			font-size: var(--text-sm);
		}

		.chip {
			font-size: var(--text-2xs);
		}

		.composer {
			padding: 7px 7px 6px;
		}

		.composer textarea {
			font-size: var(--text-sm);
		}

		.send {
			height: 30px;
			padding: 0 9px;
			font-size: var(--text-2xs);
		}

		.voice span {
			display: none;
		}

		.voice {
			width: 28px;
			justify-content: center;
			padding: 0;
		}

		.disclaimer {
			font-size: 9px;
		}
	}
</style>

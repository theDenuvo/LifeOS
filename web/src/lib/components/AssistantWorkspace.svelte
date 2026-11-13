<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import {
		confirmPendingAction,
		getConversation,
		getInsights,
		getSummary,
		listConversations,
		rejectPendingAction,
		sendAssistantMessage,
		type AssistantMessage,
		type Insight,
		type PendingAction,
		type Summary
	} from '$lib/api';
	import { errorMessage, formatTime, priorityLabel } from '$lib/format';
	import { liveRevision } from '$lib/live';

	export let basePath = '/';

	let conversationID = 0;
	let messages: AssistantMessage[] = [];
	let pending: PendingAction[] = [];
	let input = '';
	let loading = true;
	let sending = false;
	let busyAction: number | null = null;
	let error = '';
	let bootstrapped = false;
	let bootstrapping: Promise<void> | null = null;
	let handledQuery = '';

	let summary: Summary | null = null;
	let insights: Insight[] = [];
	let contextLoading = true;
	let contextError = '';
	let messagesEl: HTMLDivElement;
	let lastScreen = basePath;

	const defaultSuggestions = [
		'Что у меня на сегодня?',
		'Разбери входящие',
		'Создай задачу на завтра',
		'Помоги расставить приоритеты'
	];

	$: focus = summary?.focus_task ?? null;
	$: topInsights = insights.slice(0, 3);
	$: origin = $page.url.searchParams.get('from');
	$: screenLabel =
		origin && origin !== basePath
			? `из ${origin}`
			: basePath === '/' || basePath === '/assistant'
				? 'ассистент'
				: basePath;

	async function scrollToLatest() {
		await tick();
		if (messagesEl) {
			messagesEl.scrollTop = messagesEl.scrollHeight;
		}
	}

	async function loadConversation() {
		if (!conversationID) {
			messages = [];
			pending = [];
			return;
		}
		const view = await getConversation(conversationID);
		messages = view.messages.filter(
			(message) => message.role !== 'tool' && !(message.role === 'assistant' && !message.content.trim())
		);
		pending = view.pending_actions.filter((action) => action.status === 'pending');
	}

	async function loadContext() {
		try {
			const [nextSummary, nextInsights] = await Promise.all([getSummary(), getInsights()]);
			summary = nextSummary;
			insights = nextInsights.data;
			contextError = '';
		} catch (err) {
			contextError = errorMessage(err);
		} finally {
			contextLoading = false;
		}
	}

	async function bootstrap() {
		if (bootstrapped) return;
		if (bootstrapping) return bootstrapping;
		bootstrapping = (async () => {
			loading = true;
			try {
				const list = await listConversations();
				const latest = list.data[0];
				if (latest) {
					conversationID = latest.id;
					await loadConversation();
					await scrollToLatest();
				}
				error = '';
				bootstrapped = true;
			} catch (err) {
				error = errorMessage(err);
			} finally {
				loading = false;
				bootstrapping = null;
			}
		})();
		return bootstrapping;
	}

	async function handleQuery(query: string) {
		await bootstrap();
		input = query;
		const from = $page.url.searchParams.get('from') || basePath;
		await send(from);
		goto(basePath, { replaceState: true, noScroll: true });
	}

	$: {
		const query = $page.url.searchParams.get('q');
		if (query && query !== handledQuery) {
			handledQuery = query;
			void handleQuery(query);
		}
	}

	async function send(screen = basePath) {
		const content = input.trim();
		if (!content || sending) return;
		sending = true;
		lastScreen = screen;
		error = '';
		try {
			const result = await sendAssistantMessage(content, conversationID, { screen });
			conversationID = result.conversation_id;
			input = '';
			await loadConversation();
			await scrollToLatest();
			await loadContext();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			sending = false;
		}
	}

	function retry() {
		if (!sending) void send(lastScreen);
	}

	async function confirm(action: PendingAction) {
		busyAction = action.id;
		error = '';
		try {
			await confirmPendingAction(action.id);
			await loadConversation();
			await loadContext();
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

	function renderMessage(content: string): string {
		const escaped = content
			.replace(/&/g, '&amp;')
			.replace(/</g, '&lt;')
			.replace(/>/g, '&gt;');
		return escaped
			.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
			.replace(/`([^`]+)`/g, '<code>$1</code>')
			.replace(/\n/g, '<br>');
	}

	function useSuggestion(text: string) {
		input = text;
	}

	function newConversation() {
		conversationID = 0;
		messages = [];
		pending = [];
		input = '';
		error = '';
		goto(basePath, { replaceState: true, noScroll: true });
	}

	onMount(() => {
		bootstrap();
		loadContext();
		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0) return;
			clearTimeout(timer);
			timer = setTimeout(() => {
				loadContext();
				if (conversationID) {
					loadConversation().catch((err) => (error = errorMessage(err)));
				}
			}, 250);
		});
		return () => {
			unsubscribe();
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>Ассистент — LifeOS</title>
</svelte:head>

<div class="assistant-page">
	<header class="workspace-header">
		<div class="title-block">
			<div class="eyebrow"><span class="live-dot" aria-hidden="true"></span> LifeOS · ассистент</div>
			<h1>Ассистент</h1>
			<p class="subtitle">Ваш интерфейс к задачам, входящим и планам</p>
		</div>
		<div class="header-actions">
			{#if conversationID}
				<span class="conversation-id">диалог #{conversationID}</span>
			{/if}
			<button class="ghost-button" on:click={newConversation}>Новый диалог</button>
		</div>
	</header>

	<div class="context-strip" aria-label="Текущий контекст">
		<span class="context-chip"><span class="chip-label">экран</span> {screenLabel}</span>
		{#if summary}
			<span class="context-chip"><span class="chip-label">активных</span> {summary.stats.active_total}</span>
			<span class="context-chip"><span class="chip-label">входящих</span> {summary.stats.inbox_count}</span>
		{/if}
		{#if focus}
			<span class="context-chip context-chip--focus"><span class="chip-label">фокус</span> {focus.title}</span>
		{/if}
	</div>

	{#if error}
		<div class="error-banner" role="alert">
			<span>{error}</span>
			<div class="error-actions">
				<button class="text-button" on:click={retry} disabled={sending}>Повторить</button>
				<button class="icon-button" on:click={() => (error = '')} aria-label="Закрыть">×</button>
			</div>
		</div>
	{/if}

	<div class="workspace-grid">
		<section class="conversation" aria-label="Диалог с ассистентом">
			<div class="messages" bind:this={messagesEl} role="log" aria-live="polite">
				{#if loading}
					<div class="loading-state" aria-label="Загружаю диалог">
						<span class="skeleton-line skeleton-line--short"></span>
						<span class="skeleton-line"></span>
						<span class="skeleton-line skeleton-line--medium"></span>
					</div>
				{:else if messages.length === 0}
					<div class="empty-state">
						<p class="empty-kicker">Личный помощник</p>
						<h2>Чем займёмся?</h2>
						<p class="empty-copy">
							Скажите, что нужно сделать. Я проверю задачи и входящие, а изменения покажу до сохранения.
						</p>
						<div class="suggestions">
							{#each defaultSuggestions as suggestion}
								<button class="suggestion" on:click={() => useSuggestion(suggestion)}>
									{suggestion}
								</button>
							{/each}
						</div>
					</div>
				{:else}
					{#each messages as message (message.id)}
						<article class="turn" class:turn--user={message.role === 'user'}>
							<div class="turn-meta">
								<span class="role-mark" aria-hidden="true">{message.role === 'user' ? 'В' : 'L'}</span>
								<span class="author">{message.role === 'user' ? 'Вы' : 'LifeOS'}</span>
								<time>{formatTime(message.created_at)}</time>
							</div>
							<div class="turn-body">{@html renderMessage(message.content)}</div>
						</article>
					{/each}
				{/if}
			</div>

			{#if pending.length > 0}
				<div class="pending-list" aria-label="Действия, ожидающие подтверждения">
					{#each pending as action (action.id)}
						<article class="pending-card">
							<div class="pending-head">
								<span class="pending-badge">Требует подтверждения</span>
								<code>{action.tool_name}</code>
							</div>
							<p class="pending-summary">{action.preview.summary ?? action.tool_name}</p>
							<div class="pending-actions">
								<button class="primary-button" disabled={busyAction === action.id} on:click={() => confirm(action)}>
									Подтвердить
								</button>
								<button class="ghost-button" disabled={busyAction === action.id} on:click={() => reject(action)}>
									Отклонить
								</button>
							</div>
						</article>
					{/each}
				</div>
			{/if}

			<form class="composer" on:submit|preventDefault={() => send()}>
				<textarea
					bind:value={input}
					rows="2"
					placeholder="Напишите, что нужно сделать…"
					aria-label="Сообщение ассистенту"
					on:keydown={(event) => {
						if (event.key === 'Enter' && !event.shiftKey) {
							event.preventDefault();
							send();
						}
					}}
				></textarea>
				<div class="composer-footer">
					<span class="composer-hint">
						{sending ? 'Думаю…' : 'Enter — отправить · Shift+Enter — новая строка'}
					</span>
					<button class="send-button" type="submit" disabled={sending || !input.trim()}>
						{sending ? 'Думаю…' : 'Отправить'}
					</button>
				</div>
			</form>
		</section>

		<aside class="context-rail" aria-label="Состояние системы">
			<section class="rail-section">
				<div class="rail-heading">
					<h2>Сейчас</h2>
					<a href="/today">Сегодня →</a>
				</div>
				{#if contextLoading}
					<div class="rail-skeleton">
						<span class="skeleton-line"></span>
						<span class="skeleton-line skeleton-line--short"></span>
					</div>
				{:else if contextError}
					<p class="rail-error">{contextError}</p>
				{:else if summary}
					<div class="rail-stats">
						<div class="rail-stat">
							<span class="rail-value">{summary.stats.active_total}</span>
							<span class="rail-label">активных</span>
						</div>
						<div class="rail-stat rail-stat--danger">
							<span class="rail-value">{summary.stats.overdue_count}</span>
							<span class="rail-label">просрочено</span>
						</div>
						<div class="rail-stat">
							<span class="rail-value">{summary.stats.inbox_count}</span>
							<span class="rail-label">входящих</span>
						</div>
					</div>
				{/if}
			</section>

			{#if focus}
				<section class="rail-section">
					<div class="rail-heading">
						<h2>Фокус</h2>
						<span class="priority-pill" data-priority={focus.priority}>{priorityLabel(focus.priority)}</span>
					</div>
					<div class="focus-card">
						<p class="focus-title">{focus.title}</p>
						<p class="focus-meta">{focus.time === '—' ? 'Без точного времени' : `Сегодня в ${focus.time}`}</p>
						<a class="rail-link" href="/today">Открыть день →</a>
					</div>
				</section>
			{/if}

			{#if topInsights.length > 0}
				<section class="rail-section">
					<div class="rail-heading">
						<h2>Наблюдения</h2>
						<a href="/today">Все →</a>
					</div>
					<ul class="insight-list">
						{#each topInsights as insight (insight.key)}
							<li class="insight-item" data-severity={insight.severity}>
								<span class="insight-dot" aria-hidden="true"></span>
								<div>
									<p class="insight-title">{insight.title}</p>
									<p class="insight-detail">{insight.detail}</p>
								</div>
							</li>
						{/each}
					</ul>
				</section>
			{/if}

			<section class="rail-section rail-section--links">
				<a class="quick-link" href="/today"><span>Сегодня</span><span aria-hidden="true">→</span></a>
				<a class="quick-link" href="/inbox"><span>Входящие</span><span>{summary?.stats.inbox_count ?? '—'}</span></a>
				<a class="quick-link" href="/tasks"><span>Задачи</span><span>{summary?.stats.active_total ?? '—'}</span></a>
			</section>
		</aside>
	</div>
</div>

<style>
	.assistant-page {
		display: flex;
		min-width: 0;
		overflow-x: clip;
		flex-direction: column;
		gap: var(--space-4);
		height: calc(100dvh - 6rem);
		min-height: 560px;
	}

	.workspace-header {
		display: flex;
		min-width: 0;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-4);
	}

	.title-block {
		min-width: 0;
	}

	.eyebrow {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin-bottom: var(--space-2);
		color: var(--fg-muted);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.1em;
		text-transform: uppercase;
	}

	.live-dot {
		width: 7px;
		height: 7px;
		border-radius: var(--radius-full);
		background: var(--success);
		box-shadow: 0 0 0 3px var(--success-surface);
	}

	h1 {
		margin: 0;
		font-size: var(--text-2xl);
		font-weight: 650;
		letter-spacing: -0.025em;
	}

	.subtitle {
		margin: var(--space-1) 0 0;
		color: var(--fg-muted);
		font-size: var(--text-md);
	}

	.header-actions {
		display: flex;
		min-width: 0;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		flex-shrink: 0;
	}

	.conversation-id {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-muted);
		border: 1px solid var(--border);
		border-radius: var(--radius-full);
		padding: 0.25rem 0.6rem;
	}

	.context-strip {
		display: flex;
		min-width: 0;
		flex-wrap: wrap;
		gap: var(--space-2);
		min-height: 28px;
	}

	.context-chip {
		display: inline-flex;
		min-width: 0;
		align-items: center;
		gap: 0.35rem;
		max-width: min(100%, 420px);
		padding: 0.25rem 0.55rem;
		border: 1px solid var(--border);
		border-radius: var(--radius-full);
		background: var(--bg-card);
		color: var(--fg-body);
		font-size: var(--text-sm);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.context-chip--focus {
		border-color: var(--accent-border);
		background: var(--accent-surface);
	}

	.chip-label {
		color: var(--fg-muted);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}

	.error-banner {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		padding: 0.65rem 0.8rem;
		border: 1px solid var(--danger-border);
		border-radius: var(--radius-md);
		background: var(--danger-surface);
		color: var(--danger-text);
		font-size: var(--text-sm);
	}

	.error-actions {
		display: flex;
		align-items: center;
		gap: var(--space-1);
		flex-shrink: 0;
	}

	.workspace-grid {
		display: grid;
		min-width: 0;
		grid-template-columns: minmax(0, 1fr) var(--rail-w);
		gap: var(--space-5);
		min-height: 0;
		flex: 1;
	}

	.conversation {
		display: flex;
		min-width: 0;
		flex-direction: column;
		min-height: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-xl);
		background: var(--bg-card);
		overflow: hidden;
	}

	.messages {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: var(--space-5);
		display: flex;
		flex-direction: column;
		gap: var(--space-5);
		scrollbar-width: thin;
		scrollbar-color: var(--border) transparent;
	}

	.turn {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		max-width: 88%;
		animation: turn-in var(--duration-normal) var(--ease-out);
	}

	.turn--user {
		align-self: flex-end;
		align-items: flex-end;
	}

	.turn-meta {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		color: var(--fg-muted);
		font-size: var(--text-xs);
	}

	.role-mark {
		display: grid;
		place-items: center;
		width: 20px;
		height: 20px;
		border-radius: 6px;
		background: var(--accent-surface);
		color: var(--accent-bright);
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 700;
	}

	.turn--user .role-mark {
		background: var(--bg-raised);
		color: var(--fg-muted);
	}

	.author {
		color: var(--fg-body);
		font-weight: 600;
	}

	.turn-body :global(strong) {
		color: var(--fg-primary);
		font-weight: 650;
	}

	.turn-body :global(code) {
		padding: 1px 4px;
		border-radius: 4px;
		background: var(--bg-primary);
		font-family: var(--font-mono);
		font-size: 0.9em;
	}

	.turn-body {
		padding: 0.85rem 1rem;
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--bg-raised);
		font-size: var(--text-lg);
		line-height: 1.6;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}

	.turn--user .turn-body {
		border-color: var(--accent-border);
		background: var(--accent-surface);
		font-size: var(--text-md);
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

	.empty-state {
		margin: auto;
		max-width: 560px;
		padding: var(--space-6) var(--space-4);
		text-align: left;
	}

	.empty-kicker {
		margin: 0 0 var(--space-2);
		color: var(--accent-bright);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.12em;
	}

	.empty-state h2 {
		margin: 0;
		font-size: var(--text-3xl);
		letter-spacing: -0.03em;
	}

	.empty-copy {
		margin: var(--space-3) 0 var(--space-5);
		color: var(--fg-muted);
		font-size: var(--text-lg);
		line-height: 1.6;
	}

	.suggestions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}

	.suggestion {
		padding: 0.5rem 0.7rem;
		border: 1px solid var(--border);
		border-radius: var(--radius-full);
		background: var(--bg-raised);
		color: var(--fg-body);
		font-size: var(--text-sm);
		transition:
			border-color var(--duration-fast),
			background var(--duration-fast);
	}

	.suggestion:hover {
		border-color: var(--accent);
		background: var(--accent-surface);
	}

	.loading-state,
	.rail-skeleton {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		padding: var(--space-4) 0;
	}

	.skeleton-line {
		display: block;
		width: 100%;
		height: 12px;
		border-radius: var(--radius-full);
		background: linear-gradient(90deg, var(--bg-raised), var(--bg-hover), var(--bg-raised));
		background-size: 200% 100%;
		animation: shimmer 1.4s infinite linear;
	}

	.skeleton-line--short {
		width: 32%;
	}

	.skeleton-line--medium {
		width: 64%;
	}

	@keyframes shimmer {
		from {
			background-position: 200% 0;
		}
		to {
			background-position: -200% 0;
		}
	}

	.pending-list {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		padding: 0 var(--space-5) var(--space-3);
	}

	.pending-card {
		padding: var(--space-4);
		border: 1px solid var(--warning-border);
		border-radius: var(--radius-lg);
		background: var(--warning-surface);
	}

	.pending-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}

	.pending-badge {
		color: var(--warning);
		font-size: var(--text-xs);
		font-weight: 650;
		letter-spacing: 0.06em;
		text-transform: uppercase;
	}

	.pending-head code {
		color: var(--fg-muted);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}

	.pending-summary {
		margin: var(--space-2) 0 var(--space-3);
		font-size: var(--text-md);
		line-height: 1.5;
	}

	.pending-actions {
		display: flex;
		gap: var(--space-2);
	}

	.primary-button,
	.ghost-button,
	.send-button,
	.text-button,
	.icon-button {
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-weight: 600;
		transition:
			background var(--duration-fast),
			border-color var(--duration-fast),
			color var(--duration-fast);
	}

	.primary-button {
		padding: 0.5rem 0.85rem;
		border: 1px solid var(--accent);
		background: var(--accent);
		color: var(--on-accent);
	}

	.primary-button:hover:not(:disabled) {
		background: var(--accent-bright);
		border-color: var(--accent-bright);
	}

	.ghost-button {
		padding: 0.5rem 0.85rem;
		border: 1px solid var(--border);
		background: transparent;
		color: var(--fg-muted);
	}

	.ghost-button:hover:not(:disabled) {
		color: var(--fg-primary);
		border-color: var(--ink-700);
	}

	.text-button {
		padding: 0.25rem 0.45rem;
		border: none;
		background: transparent;
		color: var(--danger-text);
	}

	.text-button:hover:not(:disabled) {
		background: var(--danger-surface);
	}

	.icon-button {
		display: grid;
		place-items: center;
		width: 28px;
		height: 28px;
		border: none;
		background: transparent;
		color: inherit;
		font-size: var(--text-lg);
	}

	.primary-button:disabled,
	.ghost-button:disabled,
	.send-button:disabled {
		opacity: 0.45;
	}

	.composer {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-3);
		border-top: 1px solid var(--border);
		background: var(--bg-card);
	}

	.composer textarea {
		width: 100%;
		min-height: 64px;
		max-height: 180px;
		resize: vertical;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
		padding: 0.7rem 0.8rem;
		font-size: var(--text-md);
		line-height: 1.5;
	}

	.composer textarea:focus {
		border-color: var(--accent);
		box-shadow: 0 0 0 2px var(--accent-surface);
	}

	.composer-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}

	.composer-hint {
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	.send-button {
		padding: 0.55rem 0.95rem;
		border: 1px solid var(--accent);
		background: var(--accent);
		color: var(--on-accent);
	}

	.send-button:hover:not(:disabled) {
		background: var(--accent-bright);
		border-color: var(--accent-bright);
	}

	.context-rail {
		min-width: 0;
		min-height: 0;
		overflow-y: auto;
		padding-right: var(--space-1);
		display: flex;
		flex-direction: column;
		gap: var(--space-5);
		scrollbar-width: thin;
		scrollbar-color: var(--border) transparent;
	}

	.rail-section {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	.rail-heading {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2);
	}

	.rail-heading h2 {
		margin: 0;
		color: var(--fg-muted);
		font-size: var(--text-xs);
		font-weight: 650;
		letter-spacing: 0.1em;
		text-transform: uppercase;
	}

	.rail-heading a,
	.rail-link {
		color: var(--accent-bright);
		font-size: var(--text-xs);
		text-decoration: none;
	}

	.rail-heading a:hover,
	.rail-link:hover {
		text-decoration: underline;
	}

	.rail-stats {
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		gap: var(--space-2);
	}

	.rail-stat {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-card);
	}

	.rail-value {
		font-family: var(--font-mono);
		font-size: var(--text-xl);
		font-weight: 700;
		font-variant-numeric: tabular-nums;
	}

	.rail-label {
		color: var(--fg-muted);
		font-size: var(--text-xs);
	}

	.rail-stat--danger .rail-value {
		color: var(--danger);
	}

	.focus-card {
		padding: var(--space-3);
		border: 1px solid var(--accent-border);
		border-radius: var(--radius-md);
		background: var(--accent-surface);
	}

	.focus-title {
		margin: 0;
		font-size: var(--text-md);
		font-weight: 600;
		line-height: 1.4;
	}

	.focus-meta {
		margin: var(--space-1) 0 var(--space-2);
		color: var(--fg-muted);
		font-size: var(--text-xs);
	}

	.priority-pill {
		padding: 0.1rem 0.45rem;
		border: 1px solid var(--border);
		border-radius: var(--radius-full);
		color: var(--fg-muted);
		font-size: 10px;
		text-transform: lowercase;
	}

	.priority-pill[data-priority='high'] {
		color: var(--danger);
		border-color: var(--danger-border);
	}

	.priority-pill[data-priority='medium'] {
		color: var(--warning);
		border-color: var(--warning-border);
	}

	.insight-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	.insight-item {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
	}

	.insight-dot {
		width: 6px;
		height: 6px;
		margin-top: 6px;
		border-radius: var(--radius-full);
		background: var(--fg-faint);
		flex-shrink: 0;
	}

	.insight-item[data-severity='critical'] .insight-dot {
		background: var(--danger);
	}

	.insight-item[data-severity='warning'] .insight-dot {
		background: var(--warning);
	}

	.insight-item[data-severity='info'] .insight-dot {
		background: var(--info);
	}

	.insight-title {
		margin: 0;
		font-size: var(--text-sm);
		font-weight: 550;
		line-height: 1.4;
	}

	.insight-detail {
		margin: 2px 0 0;
		color: var(--fg-muted);
		font-size: var(--text-xs);
		line-height: 1.45;
	}

	.rail-section--links {
		gap: 0;
		padding-top: var(--space-2);
		border-top: 1px solid var(--border);
	}

	.quick-link {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.55rem 0;
		color: var(--fg-body);
		font-size: var(--text-sm);
		text-decoration: none;
		border-bottom: 1px solid var(--border-soft);
	}

	.quick-link:last-child {
		border-bottom: none;
	}

	.quick-link:hover {
		color: var(--accent-bright);
		text-decoration: none;
	}

	.quick-link span:last-child {
		color: var(--fg-muted);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}

	.rail-error {
		margin: 0;
		color: var(--danger);
		font-size: var(--text-sm);
	}

	@media (max-width: 1100px) {
		.assistant-page {
			height: auto;
			min-height: 0;
		}

		.workspace-grid {
			grid-template-columns: minmax(0, 1fr);
		}

		.conversation {
			min-height: 60vh;
		}

		.messages {
			max-height: 60vh;
		}

		.context-rail {
			display: grid;
			grid-template-columns: repeat(2, minmax(0, 1fr));
			gap: var(--space-4);
			overflow: visible;
		}

		.rail-section--links {
			grid-column: 1 / -1;
		}
	}

	@media (max-width: 720px) {
		.assistant-page {
			gap: var(--space-3);
		}

		h1 {
			font-size: var(--text-xl);
		}

		.workspace-header {
			flex-direction: column;
		}

		.header-actions {
			width: 100%;
			justify-content: space-between;
		}

		.context-strip {
			gap: var(--space-1);
		}

		.context-chip--focus {
			display: none;
		}

		.context-chip {
			max-width: 100%;
			font-size: var(--text-xs);
		}

		.context-rail {
			grid-template-columns: 1fr;
		}

		.messages {
			padding: var(--space-3);
			max-height: 55vh;
		}

		.turn {
			max-width: 96%;
		}

		.turn-body {
			font-size: var(--text-md);
		}

		.composer {
			padding: var(--space-2);
		}

		.composer-hint {
			display: none;
		}

		.send-button {
			width: 100%;
		}

		.composer-footer {
			justify-content: stretch;
		}

		.empty-state {
			padding: var(--space-4) var(--space-2);
		}

		.empty-state h2 {
			font-size: var(--text-2xl);
		}

		.empty-copy {
			font-size: var(--text-md);
		}

		.pending-list {
			padding: 0 var(--space-3) var(--space-2);
		}
	}
</style>

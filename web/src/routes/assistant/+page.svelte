<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import {
		confirmPendingAction,
		getConversation,
		listConversations,
		rejectPendingAction,
		sendAssistantMessage,
		type AssistantMessage,
		type PendingAction
	} from '$lib/api';
	import { errorMessage, formatTime } from '$lib/format';
	import { liveRevision } from '$lib/live';

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

	const suggestions = [
		'Что у меня на сегодня?',
		'Покажи просроченные задачи',
		'Создай задачу купить молоко на завтра',
		'Разбери входящие'
	];

	async function scrollToLatest() {
		await tick();
		await new Promise<void>((resolve) =>
			requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
		);
		const scroller = document.scrollingElement ?? document.documentElement;
		scroller.scrollTop = scroller.scrollHeight;
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
		await send();
		goto('/assistant', { replaceState: true, noScroll: true });
	}

	$: {
		const query = $page.url.searchParams.get('q');
		if (query && query !== handledQuery) {
			handledQuery = query;
			void handleQuery(query);
		}
	}

	async function send() {
		const content = input.trim();
		if (!content || sending) return;
		sending = true;
		error = '';
		try {
			const result = await sendAssistantMessage(content, conversationID);
			conversationID = result.conversation_id;
			input = '';
			await loadConversation();
			await scrollToLatest();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			sending = false;
		}
	}

	async function confirm(action: PendingAction) {
		busyAction = action.id;
		error = '';
		try {
			await confirmPendingAction(action.id);
			await loadConversation();
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
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyAction = null;
		}
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
		goto('/assistant', { replaceState: true, noScroll: true });
	}

	onMount(() => {
		bootstrap();
		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0 || !conversationID) return;
			clearTimeout(timer);
			timer = setTimeout(() => {
				loadConversation().catch((err) => (error = errorMessage(err)));
			}, 200);
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

<header class="page-header">
	<div>
		<h1>Ассистент</h1>
		<p class="subtitle">Понимает контекст, читает данные и предлагает действия</p>
	</div>
	<div class="header-actions">
		{#if conversationID}
			<span class="conversation-id">диалог #{conversationID}</span>
		{/if}
		<button class="new-chat" on:click={newConversation}>Новый диалог</button>
	</div>
</header>

{#if error}
	<div class="banner" role="alert">
		<span>{error}</span>
		<button on:click={() => (error = '')} aria-label="Закрыть">×</button>
	</div>
{/if}

<section class="chat" aria-label="Диалог с ассистентом">
	<div class="messages" role="log" aria-live="polite">
		{#if loading}
			<p class="muted">Загружаю диалог…</p>
		{:else if messages.length === 0}
			<div class="empty">
				<p>Спросите о задачах, планах или входящих.</p>
				<p class="muted">
					Ассистент читает данные через инструменты, а изменения предлагает подтвердить.
				</p>
				<div class="suggestions">
					{#each suggestions as suggestion}
						<button class="suggestion" on:click={() => useSuggestion(suggestion)}>{suggestion}</button>
					{/each}
				</div>
			</div>
		{:else}
			{#each messages as message (message.id)}
				<article class="bubble" class:user={message.role === 'user'}>
					<div class="bubble-head">
						<span class="author">{message.role === 'user' ? 'Вы' : 'LifeOS'}</span>
						<span class="time">{formatTime(message.created_at)}</span>
					</div>
					<p class="content">{message.content}</p>
				</article>
			{/each}
		{/if}
	</div>

	{#if pending.length > 0}
		<div class="pending" aria-label="Действия, ожидающие подтверждения">
			{#each pending as action (action.id)}
				<div class="pending-card">
					<div class="pending-main">
						<span class="pending-label">Требует подтверждения</span>
						<p class="pending-summary">{action.preview.summary ?? action.tool_name}</p>
						<code class="pending-tool">{action.tool_name}</code>
					</div>
					<div class="pending-actions">
						<button
							class="primary"
							disabled={busyAction === action.id}
							on:click={() => confirm(action)}
						>
							Подтвердить
						</button>
						<button class="ghost" disabled={busyAction === action.id} on:click={() => reject(action)}>
							Отклонить
						</button>
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<form class="composer" on:submit|preventDefault={send}>
		<textarea
			bind:value={input}
			rows="2"
			placeholder="Спросите что-нибудь или попросите изменить задачу…"
			aria-label="Сообщение ассистенту"
			on:keydown={(event) => {
				if (event.key === 'Enter' && !event.shiftKey) {
					event.preventDefault();
					send();
				}
			}}
		></textarea>
		<button type="submit" disabled={sending || !input.trim()}>
			{sending ? 'Думаю…' : 'Отправить'}
		</button>
	</form>
</section>

<style>
	.page-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 1rem;
		margin-bottom: 1.25rem;
	}

	h1 {
		margin: 0;
		font-size: 1.9rem;
		letter-spacing: -0.02em;
	}

	.subtitle {
		margin: 0.25rem 0 0;
		color: var(--fg-muted);
		font-size: 0.9rem;
	}

	.header-actions {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	.new-chat {
		border: 1px solid var(--border);
		background: transparent;
		color: var(--fg-muted);
		border-radius: 999px;
		padding: 0.3rem 0.7rem;
		font-size: 0.75rem;
		cursor: pointer;
	}

	.new-chat:hover {
		color: var(--fg-primary);
		border-color: rgba(139, 92, 246, 0.4);
	}

	.conversation-id {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.72rem;
		color: var(--fg-muted);
		border: 1px solid var(--border);
		border-radius: 999px;
		padding: 0.2rem 0.6rem;
	}

	.banner {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 1rem;
		padding: 0.7rem 0.9rem;
		margin-bottom: 1rem;
		border: 1px solid rgba(248, 113, 113, 0.4);
		background: rgba(248, 113, 113, 0.08);
		color: #fca5a5;
		border-radius: 10px;
		font-size: 0.85rem;
	}

	.banner button {
		background: none;
		border: none;
		color: inherit;
		font-size: 1.1rem;
		cursor: pointer;
	}

	.chat {
		display: flex;
		flex-direction: column;
		gap: 1rem;
	}

	.messages {
		display: flex;
		flex-direction: column;
		gap: 0.7rem;
		min-height: 240px;
	}

	.bubble {
		max-width: 78%;
		padding: 0.75rem 0.95rem;
		border: 1px solid var(--border);
		border-radius: 14px 14px 14px 4px;
		background: var(--bg-card);
	}

	.bubble.user {
		align-self: flex-end;
		border-radius: 14px 14px 4px 14px;
		border-color: rgba(139, 92, 246, 0.35);
		background: rgba(139, 92, 246, 0.12);
	}

	.bubble-head {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin-bottom: 0.3rem;
	}

	.author {
		font-size: 0.72rem;
		font-weight: 600;
		color: #c4b5fd;
	}

	.time {
		font-size: 0.68rem;
		color: var(--fg-muted);
	}

	.content {
		margin: 0;
		font-size: 0.9rem;
		line-height: 1.55;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}

	.pending {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}

	.pending-card {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		padding: 0.85rem 1rem;
		border: 1px solid rgba(251, 191, 36, 0.35);
		background: rgba(251, 191, 36, 0.06);
		border-radius: 12px;
	}

	.pending-label {
		font-size: 0.68rem;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: #fcd34d;
	}

	.pending-summary {
		margin: 0.25rem 0;
		font-size: 0.9rem;
	}

	.pending-tool {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.7rem;
		color: var(--fg-muted);
	}

	.pending-actions {
		display: flex;
		gap: 0.4rem;
		flex-shrink: 0;
	}

	.primary {
		background: linear-gradient(135deg, #8b5cf6, #06b6d4);
		color: #0a0a0f;
		border: none;
		border-radius: 9px;
		padding: 0.5rem 0.85rem;
		font-weight: 600;
		font-size: 0.82rem;
		cursor: pointer;
	}

	.primary:disabled {
		opacity: 0.55;
		cursor: default;
	}

	.ghost {
		background: transparent;
		border: 1px solid var(--border);
		color: var(--fg-muted);
		border-radius: 9px;
		padding: 0.5rem 0.75rem;
		font-size: 0.82rem;
		cursor: pointer;
	}

	.ghost:hover:not(:disabled) {
		color: var(--fg-primary);
	}

	.composer {
		display: flex;
		gap: 0.5rem;
		align-items: flex-end;
		padding: 0.6rem;
		border: 1px solid var(--border);
		border-radius: 14px;
		background: var(--bg-card);
	}

	.composer textarea {
		flex: 1;
		resize: none;
		border: none;
		background: transparent;
		padding: 0.4rem 0.5rem;
		font-size: 0.9rem;
		line-height: 1.45;
	}

	.composer textarea:focus {
		outline: none;
		box-shadow: none;
	}

	.composer button {
		background: linear-gradient(135deg, #8b5cf6, #06b6d4);
		color: #0a0a0f;
		border: none;
		border-radius: 10px;
		padding: 0.6rem 1rem;
		font-weight: 600;
		cursor: pointer;
	}

	.composer button:disabled {
		opacity: 0.55;
		cursor: default;
	}

	.empty {
		padding: 1.5rem 1rem;
		border: 1px dashed var(--border);
		border-radius: 12px;
		text-align: center;
	}

	.empty p {
		margin: 0.25rem 0;
	}

	.suggestions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.4rem;
		justify-content: center;
		margin-top: 0.9rem;
	}

	.suggestion {
		background: transparent;
		border: 1px solid var(--border);
		color: var(--fg-muted);
		border-radius: 999px;
		padding: 0.35rem 0.7rem;
		font-size: 0.78rem;
		cursor: pointer;
	}

	.suggestion:hover {
		color: var(--fg-primary);
		border-color: #4c4c63;
	}

	.muted {
		color: var(--fg-muted);
		font-size: 0.85rem;
	}

	@media (max-width: 640px) {
		.bubble {
			max-width: 92%;
		}

		.pending-card {
			flex-direction: column;
			align-items: stretch;
		}

		.pending-actions {
			justify-content: flex-end;
		}
	}
</style>

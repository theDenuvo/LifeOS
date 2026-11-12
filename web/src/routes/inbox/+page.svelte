<script lang="ts">
	import { onMount } from 'svelte';
	import { captureInbox, convertInbox, dismissInbox, listInbox, type InboxItem } from '$lib/api';
	import { errorMessage, formatTime } from '$lib/format';
	import { liveRevision } from '$lib/live';

	let items: InboxItem[] = [];
	let loading = true;
	let error = '';
	let text = '';
	let busyId: number | null = null;
	let saving = false;
	let notice = '';

	async function load() {
		loading = true;
		try {
			const result = await listInbox(true);
			items = result.data;
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	async function capture() {
		const value = text.trim();
		if (!value || saving) return;
		saving = true;
		try {
			await captureInbox(value);
			text = '';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			saving = false;
		}
	}

	async function convert(item: InboxItem) {
		busyId = item.id;
		try {
			const task = await convertInbox(item.id);
			notice = `Создана задача «${task.title}»`;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function dismiss(item: InboxItem) {
		busyId = item.id;
		try {
			await dismissInbox(item.id);
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	onMount(() => {
		load();
		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0) return;
			clearTimeout(timer);
			timer = setTimeout(load, 200);
		});
		return () => {
			unsubscribe();
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>Входящие — LifeOS</title>
</svelte:head>

<header class="page-header">
	<div>
		<h1>Входящие</h1>
		<p class="subtitle">Быстрый захват → структурирование в задачи</p>
	</div>
	<span class="count">{items.length}</span>
</header>

{#if error}
	<div class="banner" role="alert">
		<span>{error}</span>
		<button on:click={() => (error = '')} aria-label="Закрыть">×</button>
	</div>
{/if}

{#if notice}
	<div class="notice" role="status">
		<span>{notice}</span>
		<a href="/tasks">Открыть задачи</a>
		<button on:click={() => (notice = '')} aria-label="Закрыть">×</button>
	</div>
{/if}

<form class="capture" on:submit|preventDefault={capture}>
	<input
		bind:value={text}
		placeholder="Задача, мысль, ссылка, идея…"
		aria-label="Новый элемент во входящие"
		autocomplete="off"
	/>
	<button type="submit" disabled={saving || !text.trim()}>{saving ? 'Сохраняю…' : 'Добавить'}</button>
</form>

{#if loading && items.length === 0}
	<p class="muted">Загружаю…</p>
{:else if items.length === 0}
	<div class="empty">
		<p>Входящие пусты.</p>
		<p class="muted">Всё, что приходит в голову, сначала попадает сюда — разберёте позже.</p>
	</div>
{:else}
	<ul class="list">
		{#each items as item}
			<li>
				<div class="main">
					<p class="text">{item.text}</p>
					<span class="meta">Добавлено в {formatTime(item.created_at)}</span>
				</div>
				<div class="actions">
					<button class="primary" disabled={busyId === item.id} on:click={() => convert(item)}>
						В задачу
					</button>
					<button class="ghost" disabled={busyId === item.id} on:click={() => dismiss(item)}>Удалить</button>
				</div>
			</li>
		{/each}
	</ul>
{/if}

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

	.count {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.8rem;
		color: #c4b5fd;
		border: 1px solid rgba(139, 92, 246, 0.35);
		border-radius: 999px;
		padding: 0.2rem 0.6rem;
	}

	.banner,
	.notice {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.7rem 0.9rem;
		margin-bottom: 1rem;
		border-radius: 10px;
		font-size: 0.85rem;
	}

	.banner {
		border: 1px solid rgba(248, 113, 113, 0.4);
		background: rgba(248, 113, 113, 0.08);
		color: #fca5a5;
		justify-content: space-between;
	}

	.notice {
		border: 1px solid rgba(34, 197, 94, 0.35);
		background: rgba(34, 197, 94, 0.08);
		color: #86efac;
	}

	.notice a {
		color: #86efac;
		margin-left: auto;
	}

	.banner button,
	.notice button {
		background: none;
		border: none;
		color: inherit;
		font-size: 1.1rem;
		cursor: pointer;
	}

	.capture {
		display: flex;
		gap: 0.5rem;
		margin-bottom: 1.5rem;
	}

	.capture input {
		flex: 1;
		padding: 0.7rem 0.9rem;
		border-radius: 10px;
	}

	.capture button,
	.primary {
		background: linear-gradient(135deg, #8b5cf6, #06b6d4);
		color: #0a0a0f;
		border: none;
		border-radius: 10px;
		padding: 0.6rem 0.95rem;
		font-weight: 600;
		cursor: pointer;
	}

	.capture button:disabled,
	.primary:disabled {
		opacity: 0.55;
		cursor: default;
	}

	.list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}

	.list li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		padding: 0.85rem 1rem;
		border: 1px solid var(--border);
		border-radius: 12px;
		background: var(--bg-card);
	}

	.main {
		min-width: 0;
	}

	.text {
		margin: 0;
		font-size: 0.92rem;
		line-height: 1.45;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}

	.meta {
		display: block;
		margin-top: 0.25rem;
		font-size: 0.72rem;
		color: var(--fg-muted);
	}

	.actions {
		display: flex;
		gap: 0.4rem;
		flex-shrink: 0;
	}

	.ghost {
		background: transparent;
		border: 1px solid var(--border);
		color: var(--fg-muted);
		border-radius: 8px;
		padding: 0.45rem 0.65rem;
		font-size: 0.78rem;
		cursor: pointer;
	}

	.ghost:hover:not(:disabled) {
		color: var(--fg-primary);
	}

	.empty {
		padding: 2rem 1rem;
		border: 1px dashed var(--border);
		border-radius: 12px;
		text-align: center;
	}

	.empty p {
		margin: 0.2rem 0;
	}

	.muted {
		color: var(--fg-muted);
		font-size: 0.85rem;
	}

	@media (max-width: 560px) {
		.list li {
			flex-direction: column;
			align-items: stretch;
		}

		.actions {
			justify-content: flex-end;
		}
	}
</style>

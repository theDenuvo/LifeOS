<script lang="ts">
	import { onMount } from 'svelte';
	import {
		createMemory,
		deleteMemory,
		getMemoryStats,
		listMemory,
		listMemoryConflicts,
		resolveMemoryConflict,
		updateMemory,
		type MemoryCategory,
		type MemoryConflict,
		type MemoryEntry,
		type MemoryStats
	} from '$lib/api';
	import { errorMessage, formatDate } from '$lib/format';
	import { liveRevision } from '$lib/live';
	import { Brain, Check, ChevronDown, Link2, Search, Trash2, X } from 'lucide-svelte';

	type Tab = 'personal' | 'knowledge' | 'conflicts';

	const limit = 30;

	const personalCategories: { value: MemoryCategory; label: string }[] = [
		{ value: 'preference', label: 'Предпочтение' },
		{ value: 'habit', label: 'Привычка' },
		{ value: 'circumstance', label: 'Обстоятельство' },
		{ value: 'fact', label: 'Факт' },
		{ value: 'agreement', label: 'Договорённость' },
		{ value: 'context', label: 'Контекст' },
		{ value: 'rule', label: 'Правило' }
	];

	const knowledgeCategories: { value: MemoryCategory; label: string }[] = [
		{ value: 'instruction', label: 'Инструкция' },
		{ value: 'summary', label: 'Конспект' },
		{ value: 'reference', label: 'Справка' },
		{ value: 'research', label: 'Исследование' },
		{ value: 'idea', label: 'Идея' },
		{ value: 'documentation', label: 'Документация' },
		{ value: 'note', label: 'Заметка' }
	];

	const certaintyLabels: Record<string, string> = {
		fact: 'факт',
		assumption: 'предположение',
		inference: 'вывод'
	};

	const sourceLabels: Record<string, string> = {
		user: 'пользователь',
		assistant: 'ассистент',
		conversation: 'диалог',
		document: 'документ',
		task: 'задача',
		event: 'событие',
		system: 'система'
	};

	let tab: Tab = 'personal';
	let search = '';
	let entries: MemoryEntry[] = [];
	let total = 0;
	let conflicts: MemoryConflict[] = [];
	let stats: MemoryStats = { personal: 0, knowledge: 0, pending_conflicts: 0 };
	let loading = true;
	let error = '';
	let notice = '';
	let busyId: number | null = null;
	let expandedId: number | null = null;
	let busyConflict: number | null = null;

	let showCreate = false;
	let newTitle = '';
	let newContent = '';
	let newCategory: MemoryCategory = 'preference';
	let creating = false;

	let editingId: number | null = null;
	let editTitle = '';
	let editContent = '';
	let savingEdit = false;

	/** Categories offered in the create form follow the visible area. */
	$: createCategories = tab === 'knowledge' ? knowledgeCategories : personalCategories;
	$: if (createCategories.length > 0 && !createCategories.some((category) => category.value === newCategory)) {
		newCategory = createCategories[0]?.value ?? 'preference';
	}

	async function load() {
		loading = true;
		try {
			if (tab === 'conflicts') {
				const result = await listMemoryConflicts();
				conflicts = result.data;
				entries = [];
				total = result.data.length;
			} else {
				const result = await listMemory({
					query: search.trim() || undefined,
					area: tab,
					limit
				});
				entries = result.data;
				total = result.meta.total;
				conflicts = [];
			}
			stats = await getMemoryStats();
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	function selectTab(next: Tab) {
		if (tab === next) return;
		tab = next;
		expandedId = null;
		editingId = null;
		// A message about the previous tab would be read as a state of the new one.
		notice = '';
		load();
	}

	/** Russian plural for the record count: 1 запись, 2 записи, 5 записей. */
	function records(count: number): string {
		const mod10 = count % 10;
		const mod100 = count % 100;
		if (mod10 === 1 && mod100 !== 11) return 'запись';
		if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return 'записи';
		return 'записей';
	}

	function runSearch() {
		load();
	}

	async function create() {
		const title = newTitle.trim();
		const content = newContent.trim();
		if (!title || !content || creating) return;
		creating = true;
		try {
			const result = await createMemory({ title, content, category: newCategory });
			notice = result.duplicate
				? 'Такое сведение уже сохранено — добавлен ещё один источник.'
				: result.conflict_id
					? 'Сведение отличается от сохранённого. Решение ждёт во вкладке «Противоречия».'
					: 'Сведение сохранено.';
			newTitle = '';
			newContent = '';
			showCreate = false;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			creating = false;
		}
	}

	function startEdit(entry: MemoryEntry) {
		editingId = entry.id;
		editTitle = entry.title;
		editContent = entry.content;
		expandedId = entry.id;
	}

	async function saveEdit(entry: MemoryEntry) {
		if (!editTitle.trim() || !editContent.trim() || savingEdit) return;
		savingEdit = true;
		try {
			await updateMemory(entry.id, entry.version, {
				title: editTitle.trim(),
				content: editContent.trim()
			});
			editingId = null;
			notice = 'Изменение сохранено.';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			savingEdit = false;
		}
	}

	async function remove(entry: MemoryEntry) {
		busyId = entry.id;
		try {
			await deleteMemory(entry.id, entry.version);
			notice = 'Сведение удалено.';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function resolve(conflict: MemoryConflict, resolution: 'existing' | 'candidate' | 'both') {
		busyConflict = conflict.id;
		try {
			await resolveMemoryConflict(conflict.id, { resolution });
			notice =
				resolution === 'existing'
					? 'Оставлена прежняя версия.'
					: resolution === 'candidate'
						? 'Принята новая версия, прежняя осталась в истории.'
						: 'Обе версии сохранены как действительные.';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyConflict = null;
		}
	}

	function categoryLabel(entry: MemoryEntry): string {
		const all = [...personalCategories, ...knowledgeCategories];
		return all.find((category) => category.value === entry.category)?.label ?? entry.category;
	}

	onMount(() => {
		load();
		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0) return;
			clearTimeout(timer);
			timer = setTimeout(load, 250);
		});
		return () => {
			unsubscribe();
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>Память — LifeOS</title>
</svelte:head>

<header class="page-header">
	<div>
		<h1>Память</h1>
		<p class="subtitle">Что LifeOS знает о вас и накопившихся материалах — с источниками</p>
	</div>
	<button class="primary" on:click={() => (showCreate = !showCreate)}>
		{showCreate ? 'Скрыть форму' : 'Добавить сведение'}
	</button>
</header>

<div class="tabs" role="tablist" aria-label="Разделы памяти">
	<button
		class="tab"
		class:active={tab === 'personal'}
		role="tab"
		aria-selected={tab === 'personal'}
		on:click={() => selectTab('personal')}
	>
		Личное <span class="count">{stats.personal}</span>
	</button>
	<button
		class="tab"
		class:active={tab === 'knowledge'}
		role="tab"
		aria-selected={tab === 'knowledge'}
		on:click={() => selectTab('knowledge')}
	>
		База знаний <span class="count">{stats.knowledge}</span>
	</button>
	<button
		class="tab"
		class:active={tab === 'conflicts'}
		role="tab"
		aria-selected={tab === 'conflicts'}
		on:click={() => selectTab('conflicts')}
	>
		Противоречия
		{#if stats.pending_conflicts > 0}
			<span class="count alert">{stats.pending_conflicts}</span>
		{/if}
	</button>
</div>

{#if error}
	<div class="banner" role="alert">
		<span>{error}</span>
		<button on:click={() => (error = '')} aria-label="Закрыть сообщение об ошибке">×</button>
	</div>
{/if}

{#if notice}
	<div class="notice" role="status">
		<span>{notice}</span>
		<button on:click={() => (notice = '')} aria-label="Закрыть уведомление">×</button>
	</div>
{/if}

{#if showCreate}
	<form class="create" on:submit|preventDefault={create}>
		<div class="create-row">
			<input
				bind:value={newTitle}
				placeholder={tab === 'knowledge' ? 'Название материала' : 'Тема: например «Рабочие часы»'}
				aria-label="Тема сведения"
				autocomplete="off"
			/>
			<select bind:value={newCategory} aria-label="Категория">
				{#each createCategories as category}
					<option value={category.value}>{category.label}</option>
				{/each}
			</select>
		</div>
		<textarea
			bind:value={newContent}
			rows="3"
			placeholder="Что именно известно? Одним ясным утверждением."
			aria-label="Содержание сведения"
		></textarea>
		<div class="create-actions">
			<span class="muted">Сохраняется как факт и остаётся под вашим контролем.</span>
			<button type="submit" disabled={creating || !newTitle.trim() || !newContent.trim()}>
				{creating ? 'Сохраняю…' : 'Сохранить'}
			</button>
		</div>
	</form>
{/if}

{#if tab !== 'conflicts'}
	<div class="filters">
		<label class="search-field">
			<Search size={14} strokeWidth={1.8} aria-hidden="true" />
			<input
				bind:value={search}
				on:input={runSearch}
				placeholder="Поиск по темам, содержанию и сущностям"
				aria-label="Поиск в памяти"
			/>
		</label>
		<span class="muted">{total} {records(total)}</span>
	</div>
{/if}

{#if loading && entries.length === 0 && conflicts.length === 0}
	<p class="muted" role="status">Загружаю память…</p>
{:else if tab === 'conflicts'}
	{#if conflicts.length === 0}
		<div class="empty">
			<Brain size={22} strokeWidth={1.5} aria-hidden="true" />
			<p>Противоречий нет.</p>
			<p class="muted">
				Если новые сведения расходятся с сохранёнными, LifeOS не перезаписывает их молча — они появятся здесь.
			</p>
		</div>
	{:else}
		<ul class="conflict-list">
			{#each conflicts as conflict (conflict.id)}
				<li class="conflict">
					<p class="conflict-reason">{conflict.reason}</p>
					<div class="claims">
						<div class="claim">
							<span class="claim-label">Сохранено ранее</span>
							<strong>{conflict.existing.title}</strong>
							<p>{conflict.existing.content}</p>
							<span class="muted">
								{conflict.existing.sources[0]?.label ?? 'без источника'} ·
								{certaintyLabels[conflict.existing.certainty]}
							</span>
						</div>
						<div class="claim">
							<span class="claim-label">Новое сведение</span>
							<strong>{conflict.candidate.title}</strong>
							<p>{conflict.candidate.content}</p>
							<span class="muted">
								{conflict.candidate.sources[0]?.label ?? 'без источника'} ·
								{certaintyLabels[conflict.candidate.certainty]}
							</span>
						</div>
					</div>
					<div class="conflict-actions">
						<button
							class="ghost"
							disabled={busyConflict === conflict.id}
							on:click={() => resolve(conflict, 'existing')}
						>
							Оставить прежнее
						</button>
						<button
							class="ghost"
							disabled={busyConflict === conflict.id}
							on:click={() => resolve(conflict, 'candidate')}
						>
							Принять новое
						</button>
						<button
							class="ghost"
							disabled={busyConflict === conflict.id}
							on:click={() => resolve(conflict, 'both')}
						>
							Оба верны
						</button>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
{:else if entries.length === 0}
	<div class="empty">
		<Brain size={22} strokeWidth={1.5} aria-hidden="true" />
		<p>{search.trim() ? 'Ничего не найдено.' : tab === 'knowledge' ? 'База знаний пуста.' : 'Память пуста.'}</p>
		<p class="muted">
			{search.trim()
				? 'Попробуйте другую формулировку или добавьте сведение вручную.'
				: tab === 'knowledge'
					? 'Загруженные документы индексируются автоматически; материалы можно сохранять и через ассистента.'
					: 'LifeOS запоминает устойчивые сведения из разговоров. Можно добавить и вручную.'}
		</p>
	</div>
{:else}
	<ul class="entry-list">
		{#each entries as entry (entry.id)}
			<li class="entry" class:disputed={entry.status === 'disputed'}>
				<div class="entry-head">
					<div class="entry-text">
						<div class="entry-meta">
							<span class="category">{categoryLabel(entry)}</span>
							<span class="certainty" class:assumption={entry.certainty !== 'fact'}>
								{certaintyLabels[entry.certainty]}
							</span>
							{#if entry.status === 'disputed'}
								<span class="disputed-badge">ждёт решения</span>
							{/if}
							{#if entry.entities.length > 0}
								<span class="entities">
									{#each entry.entities.slice(0, 4) as entity}
										<span class="entity">{entity}</span>
									{/each}
								</span>
							{/if}
						</div>
						<h2>{entry.title}</h2>
						{#if editingId === entry.id}
							<input class="edit-title" bind:value={editTitle} aria-label="Новая тема" />
							<textarea class="edit-content" rows="4" bind:value={editContent} aria-label="Новое содержание"></textarea>
							<div class="entry-actions">
								<button
									class="primary small"
									disabled={savingEdit || !editTitle.trim() || !editContent.trim()}
									on:click={() => saveEdit(entry)}
								>
									<Check size={13} strokeWidth={2} aria-hidden="true" /> Сохранить
								</button>
								<button class="ghost small" on:click={() => (editingId = null)}>
									<X size={13} strokeWidth={2} aria-hidden="true" /> Отмена
								</button>
							</div>
						{:else}
							<p class="content">{entry.content}</p>
						{/if}
					</div>

					<div class="entry-controls">
						<button
							class="icon"
							aria-label={expandedId === entry.id ? 'Скрыть источники' : 'Показать источники'}
							title="Источники"
							on:click={() => (expandedId = expandedId === entry.id ? null : entry.id)}
						>
							<Link2 size={14} strokeWidth={1.7} aria-hidden="true" />
							<span class="source-count">{entry.sources.length}</span>
							<span class="chevron" class:rotated={expandedId === entry.id} aria-hidden="true">
								<ChevronDown size={13} strokeWidth={1.8} />
							</span>
						</button>
						{#if editingId !== entry.id}
							<button class="ghost small" on:click={() => startEdit(entry)}>Изменить</button>
							<button
								class="icon danger"
								aria-label="Удалить сведение"
								title="Удалить"
								disabled={busyId === entry.id}
								on:click={() => remove(entry)}
							>
								<Trash2 size={14} strokeWidth={1.7} aria-hidden="true" />
							</button>
						{/if}
					</div>
				</div>

				{#if expandedId === entry.id}
					<div class="provenance">
						<div class="provenance-head">
							<span>Откуда это известно</span>
							<span class="muted">уверенность {Math.round(entry.confidence * 100)}%</span>
						</div>
						<ul>
							{#each entry.sources as source}
								<li>
									<span class="source-kind">{sourceLabels[source.kind] ?? source.kind}</span>
									<span class="source-label">{source.label}</span>
									<span class="source-date">{formatDate(source.observed_at)}</span>
									{#if source.excerpt}
										<p class="excerpt">«{source.excerpt}»</p>
									{/if}
								</li>
							{/each}
						</ul>
						<p class="provenance-note">
							Изменение сохраняется с новой версией: прежняя формулировка остаётся в истории.
						</p>
					</div>
				{/if}
			</li>
		{/each}
	</ul>
{/if}

<style>
	.page-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-4);
		margin-bottom: var(--space-5);
	}

	h1 {
		margin: 0;
		font-size: var(--text-2xl);
		letter-spacing: -0.02em;
	}

	.subtitle {
		margin: var(--space-1) 0 0;
		color: var(--fg-muted);
		font-size: var(--text-md);
	}

	.primary {
		background: var(--accent);
		color: var(--on-accent);
		border: none;
		border-radius: var(--radius-md);
		padding: 0.6rem 0.95rem;
		font-weight: 600;
	}

	.primary.small,
	.ghost.small {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		padding: 0.35rem 0.6rem;
		font-size: var(--text-sm);
	}

	.primary:disabled {
		opacity: 0.55;
		cursor: default;
	}

	.ghost {
		background: transparent;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		color: var(--fg-body);
		padding: 0.45rem 0.8rem;
		font-size: var(--text-base);
	}

	.ghost:hover {
		background: var(--bg-raised);
	}

	.ghost:disabled,
	.icon:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.tabs {
		display: flex;
		gap: var(--space-1);
		margin-bottom: var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}

	.tab {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		padding: 0.55rem 0.7rem;
		border: none;
		border-bottom: 2px solid transparent;
		background: transparent;
		color: var(--fg-muted);
		font-size: var(--text-base);
	}

	.tab:hover {
		color: var(--fg-primary);
	}

	.tab.active {
		color: var(--fg-primary);
		border-bottom-color: var(--accent);
	}

	.count {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-muted);
	}

	.count.alert {
		color: var(--warning);
	}

	.banner,
	.notice {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		justify-content: space-between;
		padding: 0.7rem 0.9rem;
		margin-bottom: var(--space-4);
		border-radius: var(--radius-md);
		font-size: var(--text-base);
	}

	.banner {
		border: 1px solid var(--danger-border);
		background: var(--danger-surface);
		color: var(--danger-text);
	}

	.notice {
		border: 1px solid var(--success-surface);
		background: var(--success-surface);
		color: var(--success);
	}

	.banner button,
	.notice button {
		background: none;
		border: none;
		color: inherit;
		font-size: 1.1rem;
		line-height: 1;
	}

	.create {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-4);
		margin-bottom: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--bg-card);
	}

	.create-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: var(--space-2);
	}

	.create textarea {
		resize: vertical;
		min-height: 4.5rem;
	}

	.create-actions {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}

	.filters {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		margin-bottom: var(--space-4);
	}

	.search-field {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		flex: 1;
		padding: 0 var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
		color: var(--fg-muted);
	}

	.search-field input {
		flex: 1;
		border: none;
		background: transparent;
		padding: 0.55rem 0;
	}

	.search-field input:focus {
		outline: none;
		box-shadow: none;
	}

	.search-field:focus-within {
		border-color: var(--accent);
		box-shadow: 0 0 0 2px var(--accent-surface);
	}

	.entry-list,
	.conflict-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}

	.entry,
	.conflict {
		padding: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--bg-card);
	}

	.entry.disputed {
		border-color: var(--warning-border);
	}

	.entry-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-4);
	}

	.entry-text {
		min-width: 0;
		flex: 1;
	}

	.entry-meta {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-bottom: var(--space-1);
		font-size: var(--text-xs);
		color: var(--fg-muted);
	}

	.category {
		text-transform: uppercase;
		letter-spacing: 0.06em;
		font-weight: 600;
	}

	.certainty {
		padding: 0.05rem 0.4rem;
		border-radius: var(--radius-full);
		border: 1px solid var(--border);
	}

	.certainty.assumption {
		border-color: var(--warning-border);
		color: var(--warning);
	}

	.disputed-badge {
		padding: 0.05rem 0.4rem;
		border-radius: var(--radius-full);
		border: 1px solid var(--warning-border);
		color: var(--warning);
	}

	.entities {
		display: flex;
		gap: var(--space-1);
		flex-wrap: wrap;
	}

	.entity {
		padding: 0.05rem 0.4rem;
		border-radius: var(--radius-full);
		background: var(--bg-raised);
		color: var(--fg-muted);
	}

	.entry h2 {
		margin: 0;
		font-size: var(--text-lg);
		font-weight: 600;
	}

	.content {
		margin: var(--space-1) 0 0;
		color: var(--fg-body);
		font-size: var(--text-md);
		max-width: 72ch;
		white-space: pre-wrap;
	}

	.entry-controls {
		display: flex;
		align-items: center;
		gap: var(--space-1);
		flex-shrink: 0;
	}

	.icon {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		min-width: 36px;
		min-height: 36px;
		justify-content: center;
		padding: 0 var(--space-2);
		border: 1px solid transparent;
		border-radius: var(--radius-md);
		background: transparent;
		color: var(--fg-muted);
	}

	.icon:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.icon.danger:hover {
		background: var(--danger-surface);
		color: var(--danger-text);
		border-color: var(--danger-border);
	}

	.source-count {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}

	.chevron {
		display: inline-flex;
		transition: transform var(--duration-fast) var(--ease-out);
	}

	.chevron.rotated {
		transform: rotate(180deg);
	}

	.edit-title,
	.edit-content {
		width: 100%;
		margin-top: var(--space-2);
	}

	.entry-actions {
		display: flex;
		gap: var(--space-2);
		margin-top: var(--space-2);
	}

	.provenance {
		margin-top: var(--space-3);
		padding-top: var(--space-3);
		border-top: 1px solid var(--border-soft);
	}

	.provenance-head {
		display: flex;
		justify-content: space-between;
		gap: var(--space-3);
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--fg-muted);
	}

	.provenance ul {
		list-style: none;
		margin: var(--space-2) 0 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}

	.provenance li {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		gap: var(--space-2);
		align-items: baseline;
		font-size: var(--text-sm);
	}

	.source-kind {
		color: var(--accent-bright);
	}

	.source-label {
		color: var(--fg-body);
	}

	.source-date {
		color: var(--fg-faint);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}

	.excerpt {
		grid-column: 2 / -1;
		margin: 0;
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	.provenance-note {
		margin: var(--space-3) 0 0;
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	.conflict-reason {
		margin: 0 0 var(--space-3);
		color: var(--warning);
		font-size: var(--text-base);
	}

	.claims {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-3);
	}

	.claim {
		padding: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
	}

	.claim-label {
		display: block;
		margin-bottom: var(--space-1);
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--fg-muted);
	}

	.claim strong {
		display: block;
		font-size: var(--text-base);
	}

	.claim p {
		margin: var(--space-1) 0 var(--space-2);
		font-size: var(--text-base);
		color: var(--fg-body);
	}

	.conflict-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-top: var(--space-3);
	}

	.empty {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--space-1);
		padding: var(--space-8) var(--space-5);
		border: 1px dashed var(--border);
		border-radius: var(--radius-lg);
		color: var(--fg-muted);
	}

	.empty p {
		margin: 0;
		font-size: var(--text-md);
		color: var(--fg-body);
	}

	.muted {
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	@media (max-width: 820px) {
		.entry-head {
			flex-direction: column;
		}

		.entry-controls {
			width: 100%;
			justify-content: flex-end;
		}

		.claims {
			grid-template-columns: minmax(0, 1fr);
		}

		.filters {
			flex-wrap: wrap;
		}
	}

	@media (max-width: 560px) {
		.page-header {
			flex-direction: column;
		}

		.primary,
		.ghost,
		.icon {
			min-height: 44px;
		}

		.create-row {
			grid-template-columns: minmax(0, 1fr);
		}

		.create-actions {
			flex-direction: column;
			align-items: stretch;
		}

		.provenance li {
			grid-template-columns: minmax(0, 1fr);
		}

		.excerpt {
			grid-column: 1;
		}
	}
</style>

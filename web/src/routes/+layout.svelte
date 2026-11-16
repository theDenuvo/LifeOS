<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import {
		startLive,
		conversationsRevision,
		reminderNotice,
		dismissReminder,
		outreachNotice,
		dismissOutreachNotice,
		bumpConversations
	} from '$lib/live';
	import { goto } from '$app/navigation';
	import { listConversations, createWorkspace, type Conversation } from '$lib/api';
	import { profile, loadProfile, calendarSources } from '$lib/profile';
	import { selectedCalendars, toggleCalendar } from '$lib/calendarFilter';
	import {
		CalendarDays,
		Clock,
		Inbox,
		ListTodo,
		MessageSquare,
		PanelLeftClose,
		Plus,
		Sparkles,
		BookOpen,
		Layers,
		Target
	} from 'lucide-svelte';

	const nav = [
		{ href: '/', label: 'Чат', icon: MessageSquare, chat: true },
		{ href: '/tasks', label: 'Задачи', icon: ListTodo, chat: false },
		{ href: '/goals', label: 'Цели', icon: Target, chat: false },
		{ href: '/calendar', label: 'Календарь', icon: CalendarDays, chat: false },
		{ href: '/memory', label: 'Память', icon: BookOpen, chat: false }
	];

	const mobileNav = [
		{ href: '/', label: 'Чат', icon: MessageSquare, chat: true },
		{ href: '/today', label: 'Сегодня', icon: Clock, chat: false },
		{ href: '/inbox', label: 'Входящие', icon: Inbox, chat: false },
		{ href: '/tasks', label: 'Задачи', icon: ListTodo, chat: false },
		{ href: '/goals', label: 'Цели', icon: Target, chat: false },
		{ href: '/calendar', label: 'Календарь', icon: CalendarDays, chat: false },
		{ href: '/memory', label: 'Память', icon: BookOpen, chat: false }
	];

	let conversations: Conversation[] = [];
	let workspaces: Conversation[] = [];
	let workspaceFormOpen = false;
	let workspaceTitle = '';
	let workspacePurpose = '';
	let workspaceCreating = false;
	let workspaceError = '';
	let collapsed = false;

	$: current = $page.url.pathname;
	$: isCalendar = current === '/calendar';
	$: flush = current === '/' || current === '/assistant' || current === '/calendar';

	function isActive(item: (typeof nav)[number]): boolean {
		if (item.chat) return current === '/' || current === '/assistant';
		return current === item.href;
	}

	function isMobileActive(item: (typeof mobileNav)[number]): boolean {
		if (item.chat) return current === '/' || current === '/assistant';
		return current === item.href;
	}

	async function loadConversations() {
		try {
			const [dialogs, projects] = await Promise.all([
				listConversations('main'),
				listConversations('project')
			]);
			conversations = dialogs.data;
			workspaces = projects.data;
		} catch {
			conversations = [];
			workspaces = [];
		}
	}

	async function createProjectWorkspace() {
		const title = workspaceTitle.trim();
		if (!title || workspaceCreating) return;
		workspaceCreating = true;
		workspaceError = '';
		try {
			const workspace = await createWorkspace(title, workspacePurpose.trim());
			workspaceFormOpen = false;
			workspaceTitle = '';
			workspacePurpose = '';
			bumpConversations();
			await goto(`/?conversation=${workspace.id}`);
		} catch (err) {
			workspaceError = err instanceof Error ? err.message : 'Не удалось создать пространство';
		} finally {
			workspaceCreating = false;
		}
	}

	onMount(() => {
		startLive();
		void loadProfile();
		loadConversations();
		const unsubscribe = conversationsRevision.subscribe(() => loadConversations());
		return unsubscribe;
	});
</script>

<div class="shell" class:collapsed>
	<aside class="sidebar">
		<div class="workspace-row">
			<span class="workspace-label">Рабочее пространство</span>
			<button
				class="collapse"
				type="button"
				aria-label={collapsed ? 'Развернуть панель' : 'Свернуть панель'}
				title={collapsed ? 'Развернуть панель' : 'Свернуть панель'}
				on:click={() => (collapsed = !collapsed)}
			>
				<PanelLeftClose size={14} strokeWidth={1.7} />
			</button>
		</div>

		<a class="brand" href="/" aria-label="Ассистент">
			<span class="brand-mark"><Sparkles size={18} strokeWidth={1.7} /></span>
			<span class="brand-name">Ассистент</span>
		</a>

		<a class="new-chat" href="/?new=1" title="Новый диалог">
			<Plus size={14} strokeWidth={2} />
			<span>Новый диалог</span>
		</a>

		<nav class="primary-nav" aria-label="Основная навигация">
			{#each nav as item}
				<a
					class="nav-item"
					class:active={isActive(item)}
					href={item.href}
					aria-current={isActive(item) ? 'page' : undefined}
					title={item.label}
				>
					<svelte:component this={item.icon} size={16} strokeWidth={1.7} />
					<span>{item.label}</span>
				</a>
			{/each}
		</nav>

		{#if isCalendar}
			<div class="side-section calendars">
				<h2>Мои календари</h2>
				{#each calendarSources as source}
					<button
						type="button"
						class="calendar-source"
						class:off={!$selectedCalendars.includes(source.id)}
						on:click={() => toggleCalendar(source.id)}
						aria-pressed={$selectedCalendars.includes(source.id)}
					>
						<span class="source-dot" style={`background:${source.color}`} aria-hidden="true"></span>
						<span>{source.label}</span>
					</button>
				{/each}
			</div>

			<aside class="side-hint">
				<div class="hint-title"><Sparkles size={13} strokeWidth={1.7} /> Ассистент подсказывает</div>
				<p>В этом месяце есть открытые задачи с дедлайном — стоит распределить их по неделе.</p>
			</aside>
		{:else}
			<div class="side-section recent">
				<h2>Недавние диалоги</h2>
				{#each conversations.slice(0, 5) as conversation}
					<a class="recent-item" href={`/?conversation=${conversation.id}`} title={conversation.title ?? undefined}>
						{conversation.title || `Диалог ${conversation.id}`}
					</a>
				{:else}
					<p class="recent-empty">Диалогов пока нет</p>
				{/each}
			</div>

			<div class="side-section workspaces">
				<h2>Рабочие пространства</h2>
				{#each workspaces.slice(0, 5) as workspace (workspace.id)}
					<a
						class="recent-item workspace-item"
						href={`/?conversation=${workspace.id}`}
						title={workspace.purpose || workspace.title || undefined}
					>
						<Layers size={12} strokeWidth={1.8} aria-hidden="true" />
						<span>{workspace.title || `Пространство ${workspace.id}`}</span>
					</a>
				{/each}
				{#if workspaceFormOpen}
					<form class="workspace-form" on:submit|preventDefault={createProjectWorkspace}>
						<input
							class="workspace-input"
							type="text"
							bind:value={workspaceTitle}
							placeholder="Название темы"
							aria-label="Название рабочего пространства"
							maxlength="80"
							required
						/>
						<textarea
							class="workspace-input"
							bind:value={workspacePurpose}
							placeholder="О чём будет работа (необязательно)"
							aria-label="Тема рабочего пространства"
							rows="2"
							maxlength="500"
						></textarea>
						{#if workspaceError}<p class="workspace-error" role="alert">{workspaceError}</p>{/if}
						<div class="workspace-form-actions">
							<button
								type="button"
								class="workspace-cancel"
								on:click={() => (workspaceFormOpen = false)}>Отмена</button
							>
							<button
								type="submit"
								class="workspace-submit"
								disabled={workspaceCreating || !workspaceTitle.trim()}
							>
								{workspaceCreating ? 'Создаю…' : 'Создать'}
							</button>
						</div>
					</form>
				{:else}
					<button
						type="button"
						class="workspace-add"
						on:click={() => {
							workspaceFormOpen = true;
							workspaceError = '';
						}}
					>
						<Plus size={12} strokeWidth={2} aria-hidden="true" />
						<span>Новое пространство</span>
					</button>
				{/if}
			</div>
		{/if}

		<div class="side-links">
			<a href="/today"><Clock size={11} strokeWidth={1.8} />Сегодня</a>
			<span aria-hidden="true">·</span>
			<a href="/inbox"><Inbox size={11} strokeWidth={1.8} />Входящие</a>
		</div>

		<div class="profile">
			<span class="profile-avatar">{$profile.initials}</span>
			<div class="profile-text">
				<span class="profile-name">{$profile.name}</span>
				{#if $profile.role}<span class="profile-role">{$profile.role}</span>{/if}
			</div>
		</div>
	</aside>

	<main class="content" class:flush>
		<slot />
	</main>

	{#if $reminderNotice}
		<aside class="reminder-toast" role="status" aria-live="polite">			<div class="reminder-body">
				<span class="reminder-kind">
					{$reminderNotice.kind === 'overdue'
						? 'Просрочено'
						: $reminderNotice.kind === 'due'
							? 'Срок сейчас'
							: 'Скоро срок'}
				</span>
				<span class="reminder-title">{$reminderNotice.title}</span>
				{#if $reminderNotice.due_date}
					<span class="reminder-due">до {$reminderNotice.due_date}</span>
				{:else if $reminderNotice.due_at}
					<span class="reminder-due">
						до {new Date($reminderNotice.due_at).toLocaleTimeString('ru-RU', {
							hour: '2-digit',
							minute: '2-digit'
						})}
					</span>
				{/if}
			</div>
			<button class="reminder-close" on:click={dismissReminder} aria-label="Скрыть напоминание">×</button>
		</aside>
	{/if}

	{#if $outreachNotice}
		<aside class="reminder-toast outreach-toast" role="status" aria-live="polite">
			<div class="reminder-body">
				<span class="reminder-kind">Инициатива LifeOS</span>
				<span class="reminder-title">{$outreachNotice.title}</span>
				<button
					class="outreach-open"
					on:click={() => {
						dismissOutreachNotice();
						goto('/');
					}}
				>
					Открыть чат
				</button>
			</div>
			<button class="reminder-close" on:click={dismissOutreachNotice} aria-label="Скрыть обращение">×</button>
		</aside>
	{/if}

	<nav class="mobile-nav" aria-label="Мобильная навигация">
		{#each mobileNav as item}
			<a
				class="mobile-item"
				class:active={isMobileActive(item)}
				href={item.href}
				aria-current={isMobileActive(item) ? 'page' : undefined}
			>
				<svelte:component this={item.icon} size={20} strokeWidth={1.6} />
				<span>{item.label}</span>
			</a>
		{/each}
	</nav>
</div>

<style>
	.shell {
		display: grid;
		grid-template-columns: var(--sidebar-w) minmax(0, 1fr);
		min-height: 100vh;
		background: var(--bg-primary);
	}

	.sidebar {
		display: flex;
		flex-direction: column;
		min-width: 0;
		height: 100vh;
		padding: 0 12px 14px;
		border-right: 1px solid var(--border-soft);
		background: var(--bg-sidebar);
		position: sticky;
		top: 0;
	}

	.workspace-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		height: var(--topbar-h);
		padding: 0 2px 0 6px;
		color: var(--fg-muted);
	}

	.workspace-label {
		font-size: var(--text-2xs);
		letter-spacing: 0.01em;
		white-space: nowrap;
	}

	.collapse {
		display: grid;
		place-items: center;
		width: 28px;
		height: 28px;
		border: none;
		border-radius: 6px;
		background: transparent;
		color: var(--fg-muted);
		transition:
			background-color var(--duration-fast) var(--ease-out),
			color var(--duration-fast) var(--ease-out);
	}

	.collapse:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.brand {
		display: flex;
		align-items: center;
		gap: 9px;
		padding: 6px 4px 12px;
		color: var(--fg-primary);
		text-decoration: none;
	}

	.brand:hover {
		text-decoration: none;
	}

	.brand-mark {
		display: grid;
		place-items: center;
		width: 30px;
		height: 30px;
		border-radius: 50%;
		background: linear-gradient(140deg, #8b6dff, #5b3fd6);
		color: #fff;
		box-shadow: 0 0 0 1px rgba(154, 131, 255, 0.35) inset;
	}

	.brand-name {
		font-size: var(--text-base);
		font-weight: 600;
		letter-spacing: -0.01em;
	}

	.new-chat {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 7px;
		height: 34px;
		margin-bottom: 12px;
		border: 1px solid var(--accent-border);
		border-radius: 8px;
		background: var(--accent-surface);
		color: var(--accent-bright);
		font-size: var(--text-sm);
		font-weight: 550;
		text-decoration: none;
		transition: background var(--duration-fast), border-color var(--duration-fast);
	}

	.new-chat:hover {
		background: rgba(124, 92, 252, 0.22);
		border-color: var(--accent);
		text-decoration: none;
	}

	.primary-nav {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.nav-item {
		display: flex;
		align-items: center;
		gap: 10px;
		height: 34px;
		padding: 0 9px;
		border-radius: 7px;
		color: var(--fg-muted);
		font-size: var(--text-base);
		text-decoration: none;
		transition:
			background-color var(--duration-fast) var(--ease-out),
			color var(--duration-fast) var(--ease-out);
	}

	.nav-item:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
		text-decoration: none;
	}

	.nav-item.active {
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.nav-item.active :global(svg) {
		color: var(--accent-bright);
	}

	.side-section {
		margin-top: 22px;
		min-height: 0;
	}

	.side-section h2 {
		margin: 0 0 7px;
		padding: 0 9px;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
	}

	.recent {
		overflow: hidden;
	}

	.recent-item {
		display: block;
		padding: 5px 9px;
		border-radius: 6px;
		color: var(--fg-muted);
		font-size: var(--text-sm);
		line-height: 1.4;
		text-decoration: none;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		transition:
			background-color var(--duration-fast) var(--ease-out),
			color var(--duration-fast) var(--ease-out);
	}

	.recent-item:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
		text-decoration: none;
	}

	.recent-empty {
		margin: 0;
		padding: 0 9px;
		color: var(--fg-faint);
		font-size: var(--text-sm);
	}

	.workspaces {
		overflow: hidden;
	}

	.workspaces .recent-item {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
	}

	.workspaces .recent-item :global(svg) {
		flex: none;
		color: var(--accent-bright);
	}

	.workspaces .recent-item span {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.workspace-add {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		margin-top: 4px;
		padding: 5px 9px;
		border: 1px dashed var(--border-soft);
		border-radius: 6px;
		background: transparent;
		color: var(--fg-faint);
		font-size: var(--text-sm);
		cursor: pointer;
		transition:
			color var(--duration-fast) var(--ease-out),
			border-color var(--duration-fast) var(--ease-out);
	}

	.workspace-add:hover {
		color: var(--fg-primary);
		border-color: var(--border-strong, var(--border-soft));
	}

	.workspace-form {
		display: flex;
		flex-direction: column;
		gap: 6px;
		margin-top: 4px;
	}

	.workspace-input {
		width: 100%;
		padding: 6px 8px;
		border: 1px solid var(--border-soft);
		border-radius: 6px;
		background: var(--bg-raised);
		color: var(--fg-primary);
		font: inherit;
		font-size: var(--text-sm);
		resize: vertical;
	}

	.workspace-input:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 1px;
		border-color: var(--accent);
	}

	.workspace-error {
		margin: 0;
		color: var(--danger, #e5484d);
		font-size: var(--text-2xs, var(--text-xs));
	}

	.workspace-form-actions {
		display: flex;
		justify-content: flex-end;
		gap: 6px;
	}

	.workspace-cancel,
	.workspace-submit {
		padding: 4px 10px;
		border: none;
		border-radius: 6px;
		font: inherit;
		font-size: var(--text-sm);
		cursor: pointer;
	}

	.workspace-cancel {
		background: transparent;
		color: var(--fg-muted);
	}

	.workspace-cancel:hover {
		color: var(--fg-primary);
	}

	.workspace-submit {
		background: var(--accent);
		color: #fff;
		font-weight: 550;
	}

	.workspace-submit:disabled {
		opacity: 0.55;
		cursor: default;
	}

	.calendar-source {
		display: flex;
		width: 100%;
		align-items: center;
		gap: 8px;
		padding: 4px 9px;
		border: none;
		border-radius: 5px;
		background: transparent;
		color: var(--fg-body);
		font-size: var(--text-sm);
		text-align: left;
		transition:
			background-color var(--duration-fast) var(--ease-out),
			color var(--duration-fast) var(--ease-out),
			opacity var(--duration-fast) var(--ease-out);
	}

	.calendar-source:hover {
		background: var(--bg-raised);
	}

	.calendar-source.off {
		color: var(--fg-faint);
		opacity: 0.55;
	}

	.calendar-source.off .source-dot {
		filter: grayscale(1);
	}

	.source-dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.side-hint {
		margin-top: 18px;
		padding: 12px;
		border: 1px solid var(--border);
		border-radius: 9px;
		background: var(--bg-panel);
	}

	.hint-title {
		display: flex;
		align-items: center;
		gap: 6px;
		color: var(--accent-bright);
		font-size: var(--text-2xs);
		font-weight: 600;
	}

	.side-hint p {
		margin: 7px 0 0;
		color: var(--fg-muted);
		font-size: var(--text-xs);
		line-height: 1.5;
	}

	.side-links {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-top: auto;
		padding: 0 6px 8px;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.side-links a {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		color: var(--fg-faint);
		text-decoration: none;
	}

	.side-links a:hover {
		color: var(--fg-body);
		text-decoration: none;
	}

	.profile {
		display: flex;
		align-items: center;
		gap: 9px;
		padding: 10px 6px 2px;
		border-top: 1px solid var(--border-soft);
	}

	.profile-avatar {
		display: grid;
		place-items: center;
		width: 28px;
		height: 28px;
		border-radius: 50%;
		background: linear-gradient(140deg, #3a3a48, #22222b);
		border: 1px solid var(--border);
		color: var(--fg-body);
		font-size: 10px;
		font-weight: 600;
		flex-shrink: 0;
	}

	.profile-text {
		display: flex;
		min-width: 0;
		flex-direction: column;
		line-height: 1.3;
	}

	.profile-name {
		color: var(--fg-primary);
		font-size: var(--text-xs);
		font-weight: 550;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.profile-role {
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.content {
		min-width: 0;
		width: 100%;
		max-width: var(--content-max);
		min-height: 100vh;
		padding: 24px clamp(20px, 2.4vw, 32px) 48px;
	}

	.content.flush {
		max-width: none;
		height: 100dvh;
		min-height: 0;
		padding: 0;
		overflow: hidden;
	}

	.mobile-nav {
		display: none;
	}

	.reminder-toast {
		position: fixed;
		right: var(--space-5);
		bottom: var(--space-5);
		z-index: 40;
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
		max-width: 22rem;
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--accent-border);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
		box-shadow: var(--shadow-overlay);
		animation: pop-in var(--duration-normal) var(--ease-out);
	}

	.reminder-body {
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
	}

	.reminder-kind {
		color: var(--accent-bright);
		font-size: var(--text-2xs);
		text-transform: uppercase;
		letter-spacing: 0.08em;
	}

	.reminder-title {
		color: var(--fg-primary);
		font-size: var(--text-base);
	}

	.reminder-due {
		color: var(--fg-muted);
		font-size: var(--text-xs);
	}

	.reminder-close {
		background: none;
		border: none;
		color: var(--fg-muted);
		cursor: pointer;
		font-size: 1.1rem;
		line-height: 1;
	}

	.outreach-toast .reminder-kind {
		color: var(--accent-bright);
	}

	/* Stack above the reminder toast when both are on screen at once. */
	.outreach-toast {
		bottom: calc(var(--space-5) + 11rem);
	}

	.outreach-open {
		margin-top: var(--space-2);
		padding: var(--space-2) var(--space-4);
		border: none;
		border-radius: var(--radius-sm);
		background: var(--accent);
		color: var(--fg-primary);
		font-size: var(--text-xs);
		cursor: pointer;
	}

	.outreach-open:hover {
		filter: brightness(1.1);
	}

	.shell.collapsed {
		grid-template-columns: 64px minmax(0, 1fr);
	}

	.shell.collapsed .sidebar {
		padding: 0 8px 14px;
	}

	.shell.collapsed .workspace-label,
	.shell.collapsed .brand-name,
	.shell.collapsed .new-chat span,
	.shell.collapsed .side-section,
	.shell.collapsed .side-links,
	.shell.collapsed .profile-text {
		display: none;
	}

	.shell.collapsed .workspace-row {
		justify-content: center;
		padding: 0;
	}

	.shell.collapsed .brand {
		justify-content: center;
		padding: 6px 0 12px;
	}

	.shell.collapsed .new-chat {
		width: 34px;
		margin-inline: auto;
	}

	.shell.collapsed .nav-item {
		justify-content: center;
		padding: 0;
	}

	.shell.collapsed .nav-item span {
		display: none;
	}

	.shell.collapsed .profile {
		justify-content: center;
		margin-top: auto;
		padding-inline: 0;
	}

	@media (max-width: 1100px) {
		.content.flush {
			overflow: visible;
			height: auto;
		}
	}

	@media (max-width: 820px) {
		.shell {
			grid-template-columns: minmax(0, 1fr);
		}

		.sidebar {
			display: none;
		}

		.content {
			min-height: calc(100dvh - var(--mobile-nav-h));
			padding: 16px 16px calc(var(--mobile-nav-h) + 24px + env(safe-area-inset-bottom));
		}

		.content.flush {
			padding-bottom: calc(var(--mobile-nav-h) + env(safe-area-inset-bottom));
		}

		.mobile-nav {
			display: grid;
			grid-template-columns: repeat(6, 1fr);
			position: fixed;
			inset: auto 0 0 0;
			z-index: 30;
			padding: 4px 4px calc(4px + env(safe-area-inset-bottom));
			border-top: 1px solid var(--border);
			background: rgba(10, 10, 13, 0.97);
			backdrop-filter: blur(12px);
		}

		.mobile-item {
			display: flex;
			min-height: 44px;
			flex-direction: column;
			align-items: center;
			justify-content: center;
			gap: 2px;
			border-radius: 7px;
			color: var(--fg-muted);
			font-size: 9.5px;
			text-decoration: none;
		}

		.mobile-item.active {
			color: var(--accent-bright);
		}

		.mobile-item:hover {
			text-decoration: none;
		}
	}
</style>

import { writable, get } from 'svelte/store';
import { getProfile, type CalendarName, type Profile } from './api';

// Neutral fallback until the profile API responds (or if it is unavailable).
export const fallbackProfile: Profile = {
	name: 'Профиль',
	initials: '•',
	role: ''
};

export const profile = writable<Profile>(fallbackProfile);

let loaded = false;

export async function loadProfile(force = false): Promise<Profile> {
	if (loaded && !force) return get(profile);
	try {
		const value = await getProfile();
		profile.set(value);
		loaded = true;
	} catch {
		// Keep the fallback; the shell must still render if settings are unavailable.
	}
	return get(profile);
}

export const calendarSources: Array<{ id: CalendarName; label: string; color: string }> = [
	{ id: 'work', label: 'Работа', color: 'var(--accent)' },
	{ id: 'meetings', label: 'Встречи', color: 'var(--success)' },
	{ id: 'personal', label: 'Личное', color: 'var(--info)' }
];

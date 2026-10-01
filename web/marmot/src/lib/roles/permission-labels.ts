import { m } from '$lib/paraglide/messages';
import type { Permission } from './types';

// Static references keep check-i18n and tree-shaking working. Permissions Marmot ships keep the
// name and description the server sends; the distribution's own ones are translated here.
const groups: Record<string, () => string> = {
	domains: m.permission_group_domains,
	workflows: m.permission_group_workflows,
	metadata_quality: m.permission_group_metadata_quality
};

const names: Record<string, () => string> = {
	dgu_view_domains: m.permission_name_dgu_view_domains,
	dgu_manage_domains: m.permission_name_dgu_manage_domains,
	dgu_view_workflows: m.permission_name_dgu_view_workflows,
	dgu_manage_workflows: m.permission_name_dgu_manage_workflows,
	dgu_start_workflows: m.permission_name_dgu_start_workflows,
	dgu_metadata_quality_view: m.permission_name_dgu_metadata_quality_view,
	dgu_metadata_quality_run: m.permission_name_dgu_metadata_quality_run,
	dgu_metadata_quality_manage: m.permission_name_dgu_metadata_quality_manage
};

const descriptions: Record<string, () => string> = {
	dgu_view_domains: m.permission_desc_dgu_view_domains,
	dgu_manage_domains: m.permission_desc_dgu_manage_domains,
	dgu_view_workflows: m.permission_desc_dgu_view_workflows,
	dgu_manage_workflows: m.permission_desc_dgu_manage_workflows,
	dgu_start_workflows: m.permission_desc_dgu_start_workflows,
	dgu_metadata_quality_view: m.permission_desc_dgu_metadata_quality_view,
	dgu_metadata_quality_run: m.permission_desc_dgu_metadata_quality_run,
	dgu_metadata_quality_manage: m.permission_desc_dgu_metadata_quality_manage
};

export const resourceLabel = (resourceType: string): string =>
	groups[resourceType]?.() ?? resourceType;
export const permissionName = (permission: Permission): string =>
	names[permission.name]?.() ?? permission.name;
export const permissionDescription = (permission: Permission): string | undefined =>
	descriptions[permission.name]?.() ?? permission.description;

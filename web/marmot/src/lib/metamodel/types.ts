export interface MetamodelPresentation {
	labelKey?: string;
	helpTextKey?: string;
	descriptionKey?: string;
	section?: string;
	order?: number;
	/**
	 * Alternate editor for a string field's value; the stored type is unchanged. "user" holds a
	 * user ID; "glossary_term" holds glossary term IDs, in a string or a list, on terms only.
	 */
	control?: string;
	/** Names a glossary_term link seen from the term it points to. */
	inverseLabelKey?: string;
	/** Offer this field as a segmented Discover filter. Only enum and boolean fields qualify. */
	facet?: boolean;
	/** Stored value -> message key, to show a label in place of an enum or boolean value. */
	valueLabelKeys?: Record<string, string>;
	/** Show an enum field's value as a chip next to the entity's name. */
	badge?: boolean;
}

export interface MetamodelConstraints {
	minimum?: number;
	maximum?: number;
	minLength?: number;
	maxLength?: number;
	minItems?: number;
	maxItems?: number;
}

export interface MetamodelAppliesTo {
	kinds?: string[];
	assetTypes?: string[];
}

export interface MetamodelField {
	id: string;
	type: string;
	itemType?: string;
	core: boolean;
	required: boolean;
	nullable?: boolean;
	storage: string;
	appliesTo: MetamodelAppliesTo;
	values?: string[];
	validation?: MetamodelConstraints;
	presentation?: MetamodelPresentation;
}

export interface MetamodelSchema {
	formatVersion: number;
	id: string;
	version: number;
	defaultLocale: string;
	fields: MetamodelField[];
	hash: string;
	enabled: boolean;
	/** Profile-supplied catalogues by locale, resolving presentation keys to text. */
	messages?: Record<string, Record<string, string>>;
}

export interface MetamodelViolation {
	field: string;
	code: string;
}

export interface MetamodelValidationError {
	fields: MetamodelViolation[];
}

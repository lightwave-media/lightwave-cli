/**
 * Claude Code Hook Types
 *
 * Official type definitions based on Claude Code documentation:
 * https://code.claude.com/docs/en/hooks
 *
 * The @anthropic-ai/claude-code SDK exports tool input types (BashInput, etc.)
 * but not hook types. These types are derived from the official documentation.
 */

// =============================================================================
// Common Input Types (all hooks receive these fields)
// =============================================================================

/**
 * Base fields present in all hook inputs.
 * Every hook receives these fields via stdin as JSON.
 */
export interface HookInputBase {
  /** Current session identifier */
  session_id: string;
  /** Path to conversation JSON transcript */
  transcript_path: string;
  /** Current working directory when the hook is invoked */
  cwd: string;
  /** Current permission mode */
  permission_mode:
    | "default"
    | "plan"
    | "acceptEdits"
    | "dontAsk"
    | "bypassPermissions";
  /** Name of the event that fired */
  hook_event_name: string;
}

// =============================================================================
// Event-Specific Input Types
// =============================================================================

/**
 * Stop hook input - runs when Claude finishes responding.
 */
export interface StopHookInput extends HookInputBase {
  hook_event_name: "Stop";
  /** True when Claude is already continuing due to a stop hook */
  stop_hook_active: boolean;
}

/**
 * SubagentStop hook input - runs when a subagent finishes.
 */
export interface SubagentStopHookInput extends HookInputBase {
  hook_event_name: "SubagentStop";
  /** True when the agent is already continuing due to a stop hook */
  stop_hook_active: boolean;
  /** Unique identifier for the subagent */
  agent_id: string;
  /** Agent type name (Bash, Explore, Plan, or custom) */
  agent_type: string;
  /** Path to the subagent's transcript */
  agent_transcript_path: string;
}

/**
 * UserPromptSubmit hook input - runs before Claude processes a user message.
 */
export interface UserPromptSubmitHookInput extends HookInputBase {
  hook_event_name: "UserPromptSubmit";
  /** The user's prompt text */
  prompt: string;
}

/**
 * SessionStart hook input - runs when a session begins or resumes.
 */
export interface SessionStartHookInput extends HookInputBase {
  hook_event_name: "SessionStart";
  /** How the session was initiated */
  source: "startup" | "resume" | "clear" | "compact";
  /** Model identifier being used */
  model: string;
  /** Agent name if started with --agent flag */
  agent_type?: string;
}

/**
 * SessionEnd hook input - runs when a session terminates.
 */
export interface SessionEndHookInput extends HookInputBase {
  hook_event_name: "SessionEnd";
  /** Why the session ended */
  reason:
    | "clear"
    | "logout"
    | "prompt_input_exit"
    | "bypass_permissions_disabled"
    | "other";
}

/**
 * PreToolUse hook input - runs before a tool call executes.
 */
export interface PreToolUseHookInput extends HookInputBase {
  hook_event_name: "PreToolUse";
  /** Name of the tool being called */
  tool_name: string;
  /** Tool-specific input parameters */
  tool_input: Record<string, unknown>;
  /** Unique identifier for this tool use */
  tool_use_id: string;
}

/**
 * PostToolUse hook input - runs after a tool completes successfully.
 */
export interface PostToolUseHookInput extends HookInputBase {
  hook_event_name: "PostToolUse";
  /** Name of the tool that was called */
  tool_name: string;
  /** Tool-specific input parameters that were sent */
  tool_input: Record<string, unknown>;
  /** Result returned by the tool */
  tool_response: Record<string, unknown>;
  /** Unique identifier for this tool use */
  tool_use_id: string;
}

/**
 * PostToolUseFailure hook input - runs after a tool call fails.
 */
export interface PostToolUseFailureHookInput extends HookInputBase {
  hook_event_name: "PostToolUseFailure";
  /** Name of the tool that failed */
  tool_name: string;
  /** Tool-specific input parameters that were sent */
  tool_input: Record<string, unknown>;
  /** Unique identifier for this tool use */
  tool_use_id: string;
  /** Error message describing what went wrong */
  error: string;
  /** Whether the failure was caused by user interruption */
  is_interrupt?: boolean;
}

/**
 * PermissionRequest hook input - runs when a permission dialog appears.
 */
export interface PermissionRequestHookInput extends HookInputBase {
  hook_event_name: "PermissionRequest";
  /** Name of the tool requesting permission */
  tool_name: string;
  /** Tool-specific input parameters */
  tool_input: Record<string, unknown>;
  /** Available "always allow" options */
  permission_suggestions?: Array<{
    type: string;
    tool?: string;
  }>;
}

/**
 * Notification hook input - runs when Claude Code sends notifications.
 */
export interface NotificationHookInput extends HookInputBase {
  hook_event_name: "Notification";
  /** Notification message text */
  message: string;
  /** Optional notification title */
  title?: string;
  /** Type of notification */
  notification_type:
    | "permission_prompt"
    | "idle_prompt"
    | "auth_success"
    | "elicitation_dialog";
}

/**
 * SubagentStart hook input - runs when a subagent is spawned.
 */
export interface SubagentStartHookInput extends HookInputBase {
  hook_event_name: "SubagentStart";
  /** Unique identifier for the subagent */
  agent_id: string;
  /** Agent type name (Bash, Explore, Plan, or custom) */
  agent_type: string;
}

/**
 * PreCompact hook input - runs before context compaction.
 */
export interface PreCompactHookInput extends HookInputBase {
  hook_event_name: "PreCompact";
  /** What triggered compaction */
  trigger: "manual" | "auto";
  /** Custom instructions for manual compaction */
  custom_instructions: string;
}

/**
 * Union type for all possible hook inputs.
 */
export type HookInput =
  | StopHookInput
  | SubagentStopHookInput
  | UserPromptSubmitHookInput
  | SessionStartHookInput
  | SessionEndHookInput
  | PreToolUseHookInput
  | PostToolUseHookInput
  | PostToolUseFailureHookInput
  | PermissionRequestHookInput
  | NotificationHookInput
  | SubagentStartHookInput
  | PreCompactHookInput;

// =============================================================================
// Output Types
// =============================================================================

/**
 * Universal output fields available to all hooks.
 * Return these via stdout as JSON on exit code 0.
 */
export interface HookOutputBase {
  /**
   * If false, Claude stops processing entirely after the hook runs.
   * Takes precedence over any event-specific decision fields.
   * @default true
   */
  continue?: boolean;

  /**
   * Message shown to the user when continue is false.
   * Not shown to Claude.
   */
  stopReason?: string;

  /**
   * If true, hides stdout from verbose mode output.
   * @default false
   */
  suppressOutput?: boolean;

  /**
   * Warning message shown to the user.
   */
  systemMessage?: string;
}

/**
 * Stop/SubagentStop hook output - can control whether Claude continues.
 */
export interface StopHookOutput extends HookOutputBase {
  /**
   * "block" prevents Claude from stopping.
   * Omit to allow Claude to stop.
   */
  decision?: "block";

  /**
   * Required when decision is "block".
   * Tells Claude why it should continue.
   */
  reason?: string;
}

/**
 * UserPromptSubmit hook output - can block prompts or add context.
 */
export interface UserPromptSubmitHookOutput extends HookOutputBase {
  /**
   * "block" prevents the prompt from being processed.
   * Omit to allow the prompt to proceed.
   */
  decision?: "block";

  /**
   * Shown to the user when decision is "block".
   * Not added to context.
   */
  reason?: string;

  /**
   * Event-specific output for additional context.
   */
  hookSpecificOutput?: {
    hookEventName: "UserPromptSubmit";
    additionalContext?: string;
  };
}

/**
 * SessionStart hook output - can add context for Claude.
 */
export interface SessionStartHookOutput extends HookOutputBase {
  hookSpecificOutput?: {
    hookEventName: "SessionStart";
    additionalContext?: string;
  };
}

/**
 * PreToolUse hook output - can allow, deny, or modify tool calls.
 */
export interface PreToolUseHookOutput extends HookOutputBase {
  hookSpecificOutput?: {
    hookEventName: "PreToolUse";
    /**
     * "allow" bypasses the permission system.
     * "deny" prevents the tool call.
     * "ask" prompts the user to confirm.
     */
    permissionDecision?: "allow" | "deny" | "ask";
    /**
     * For allow/ask: shown to user but not Claude.
     * For deny: shown to Claude.
     */
    permissionDecisionReason?: string;
    /** Modifies the tool's input parameters before execution */
    updatedInput?: Record<string, unknown>;
    /** String added to Claude's context before the tool executes */
    additionalContext?: string;
  };
}

/**
 * PostToolUse hook output - can provide feedback to Claude.
 */
export interface PostToolUseHookOutput extends HookOutputBase {
  /**
   * "block" prompts Claude with the reason.
   * Omit to allow the action to proceed.
   */
  decision?: "block";

  /** Explanation shown to Claude when decision is "block" */
  reason?: string;

  hookSpecificOutput?: {
    hookEventName: "PostToolUse";
    additionalContext?: string;
    /** For MCP tools only: replaces the tool's output */
    updatedMCPToolOutput?: unknown;
  };
}

/**
 * PostToolUseFailure hook output - can add context about failures.
 */
export interface PostToolUseFailureHookOutput extends HookOutputBase {
  hookSpecificOutput?: {
    hookEventName: "PostToolUseFailure";
    additionalContext?: string;
  };
}

/**
 * PermissionRequest hook output - can allow or deny permissions.
 */
export interface PermissionRequestHookOutput extends HookOutputBase {
  hookSpecificOutput?: {
    hookEventName: "PermissionRequest";
    decision?: {
      /** "allow" grants permission, "deny" denies it */
      behavior: "allow" | "deny";
      /** For allow: modifies tool input before execution */
      updatedInput?: Record<string, unknown>;
      /** For allow: applies permission rule updates */
      updatedPermissions?: unknown;
      /** For deny: tells Claude why permission was denied */
      message?: string;
      /** For deny: if true, stops Claude */
      interrupt?: boolean;
    };
  };
}

/**
 * Notification hook output - can add context to conversation.
 */
export interface NotificationHookOutput extends HookOutputBase {
  hookSpecificOutput?: {
    hookEventName: "Notification";
    additionalContext?: string;
  };
}

/**
 * SubagentStart hook output - can inject context into subagent.
 */
export interface SubagentStartHookOutput extends HookOutputBase {
  hookSpecificOutput?: {
    hookEventName: "SubagentStart";
    additionalContext?: string;
  };
}

/**
 * Union type for all possible hook outputs.
 * Use the specific type for your hook event for better type safety.
 */
export type HookOutput =
  | StopHookOutput
  | UserPromptSubmitHookOutput
  | SessionStartHookOutput
  | PreToolUseHookOutput
  | PostToolUseHookOutput
  | PostToolUseFailureHookOutput
  | PermissionRequestHookOutput
  | NotificationHookOutput
  | SubagentStartHookOutput;

// =============================================================================
// Legacy Aliases (for backward compatibility)
// =============================================================================

/** @deprecated Use StopHookOutput instead */
export type HookJSONOutput = StopHookOutput;

// =============================================================================
// Helper Types
// =============================================================================

/**
 * Validation result from running checks.
 */
export interface ValidationResult {
  /** Validator name */
  name: string;
  /** Whether validation passed */
  passed: boolean;
  /** Error messages if failed */
  errors: string[];
  /** Files that were checked */
  filesChecked: string[];
}

/**
 * Categorized list of changed files.
 */
export interface ChangedFiles {
  typescript: string[];
  python: string[];
  yaml: string[];
  other: string[];
  all: string[];
}

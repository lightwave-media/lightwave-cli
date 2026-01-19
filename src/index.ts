#!/usr/bin/env node
import { Command } from "commander";
import { uiCommand } from "./commands/ui.js";
import { buildCommand } from "./commands/build.js";
import { workspaceCommand } from "./commands/workspace.js";
import { cmsCommand } from "./commands/cms.js";
import { djangoCommand } from "./commands/django.js";
import { syncCommand } from "./commands/sync.js";
import { testCommand } from "./commands/test.js";
import { islandsCommand } from "./commands/islands.js";
import { taskCommand } from "./commands/task.js";
import { sprintCommand } from "./commands/sprint.js";
import { epicCommand } from "./commands/epic.js";
import { storyCommand } from "./commands/story.js";
import { authCommand } from "./commands/auth.js";
import { docCommand } from "./commands/doc.js";
import { domainCommand } from "./commands/domain.js";
import { routeCommand } from "./commands/route.js";
import { agentCommand } from "./commands/agent.js";
import { automationCommand } from "./commands/automation.js";
import { bugsCommand } from "./commands/bugs.js";
import { notionCommand } from "./commands/notion.js";
import { outboxCommand } from "./commands/outbox.js";

const program = new Command();

program
  .name("lw")
  .description("LightWave CLI - Deterministic scaffolding and build tools")
  .version("0.0.1");

// Register command groups
program.addCommand(uiCommand);
program.addCommand(buildCommand);
program.addCommand(workspaceCommand);
program.addCommand(cmsCommand);
program.addCommand(djangoCommand);
program.addCommand(syncCommand);
program.addCommand(testCommand);
program.addCommand(islandsCommand);
program.addCommand(taskCommand);
program.addCommand(sprintCommand);
program.addCommand(epicCommand);
program.addCommand(storyCommand);
program.addCommand(authCommand);
program.addCommand(docCommand);
program.addCommand(domainCommand);
program.addCommand(routeCommand);
program.addCommand(agentCommand);
program.addCommand(automationCommand);
program.addCommand(bugsCommand);
program.addCommand(notionCommand);
program.addCommand(outboxCommand);

program.parse();

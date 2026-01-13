import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { mkdir, writeFile, readFile } from "fs/promises";
import { existsSync } from "fs";
import { join, dirname } from "path";
import Handlebars from "handlebars";
import { getPackagePath } from "../utils/paths.js";

export const uiCommand = new Command("ui").description(
  "UI component scaffolding commands",
);

/**
 * lw ui:component <category/name>
 * Creates a new component in lightwave-ui
 */
uiCommand
  .command("component <path>")
  .description("Create a new UI component (e.g., marketing/hero-cineos)")
  .option(
    "--props <props>",
    "Comma-separated props (e.g., title:string,count:number)",
  )
  .option("--variant <variant>", "Base variant to extend from")
  .option("--dry-run", "Preview what would be created without writing files")
  .action(async (componentPath: string, options) => {
    const dryRun = options.dryRun;
    const uiPath = getPackagePath("lightwave-ui");
    const parts = componentPath.split("/");
    const componentName = parts.pop()!;
    const category = parts.join("/");

    // Convert to PascalCase
    const pascalName = componentName
      .split("-")
      .map((s) => s.charAt(0).toUpperCase() + s.slice(1))
      .join("");

    // Parse props
    const props = parseProps(options.props || "");

    // Component directory
    const componentDir = join(
      uiPath,
      "src/components",
      category,
      componentName,
    );

    if (dryRun) {
      console.log(chalk.blue("\n=== Dry Run: Component Preview ===\n"));
      console.log(chalk.yellow("Would create:"));
      console.log(chalk.gray(`  ${componentDir}/${componentName}.tsx`));
      console.log(chalk.gray(`  ${componentDir}/index.ts`));
      console.log(
        chalk.gray(
          `  Update: ${join(uiPath, "src/components", category, "index.ts")}`,
        ),
      );

      console.log(chalk.yellow("\nComponent name:"), pascalName);
      console.log(
        chalk.yellow("Props:"),
        props.length
          ? props.map((p) => `${p.name}: ${p.type}`).join(", ")
          : "(none)",
      );

      console.log(chalk.yellow("\nGenerated code:\n"));
      console.log(chalk.cyan("// " + componentName + ".tsx"));
      console.log(generateComponent(pascalName, componentName, props));
      return;
    }

    const spinner = ora(`Creating component ${componentPath}`).start();

    try {
      if (existsSync(componentDir)) {
        spinner.fail(`Component already exists: ${componentDir}`);
        return;
      }

      // Create directory
      await mkdir(componentDir, { recursive: true });

      // Generate component file
      const componentContent = generateComponent(
        pascalName,
        componentName,
        props,
      );
      await writeFile(
        join(componentDir, `${componentName}.tsx`),
        componentContent,
      );

      // Generate index.ts
      const indexContent = `export { ${pascalName}, type ${pascalName}Props } from "./${componentName}.js";\n`;
      await writeFile(join(componentDir, "index.ts"), indexContent);

      // Update parent index.ts
      await updateCategoryIndex(
        join(uiPath, "src/components", category),
        componentName,
      );

      spinner.succeed(`Created component: ${componentPath}`);
      console.log(chalk.gray(`  → ${componentDir}/${componentName}.tsx`));
      console.log(chalk.gray(`  → ${componentDir}/index.ts`));
    } catch (err) {
      spinner.fail(`Failed to create component: ${err}`);
      process.exit(1);
    }
  });

/**
 * lw ui:build
 * Build the lightwave-ui package
 */
uiCommand
  .command("build")
  .description("Build the lightwave-ui package")
  .action(async () => {
    const spinner = ora("Building lightwave-ui").start();

    try {
      const uiPath = getPackagePath("lightwave-ui");
      const { exec } = await import("../utils/exec.js");
      await exec("pnpm", ["build"], { cwd: uiPath });
      spinner.succeed("Built lightwave-ui");
    } catch (err) {
      spinner.fail(`Build failed: ${err}`);
      process.exit(1);
    }
  });

/**
 * lw ui:list
 * List all components in a category
 */
uiCommand
  .command("list [category]")
  .description("List UI components (optionally filtered by category)")
  .action(async (category?: string) => {
    const uiPath = getPackagePath("lightwave-ui");
    const componentsDir = join(uiPath, "src/components");

    const { readdir } = await import("fs/promises");

    if (category) {
      const categoryPath = join(componentsDir, category);
      if (!existsSync(categoryPath)) {
        console.log(chalk.red(`Category not found: ${category}`));
        return;
      }
      const components = await readdir(categoryPath);
      console.log(chalk.blue(`\nComponents in ${category}:`));
      components
        .filter((c) => !c.endsWith(".ts"))
        .forEach((c) => console.log(`  ${c}`));
    } else {
      const categories = await readdir(componentsDir);
      console.log(chalk.blue("\nUI Component Categories:"));
      for (const cat of categories.filter((c) => !c.endsWith(".ts"))) {
        const catPath = join(componentsDir, cat);
        const components = await readdir(catPath);
        const count = components.filter((c) => !c.endsWith(".ts")).length;
        console.log(`  ${cat}/ (${count} components)`);
      }
    }
  });

// Helper functions

interface PropDef {
  name: string;
  type: string;
  optional: boolean;
}

function parseProps(propsString: string): PropDef[] {
  if (!propsString) return [];
  return propsString.split(",").map((p) => {
    const [name, type = "string"] = p.trim().split(":");
    return { name, type, optional: true };
  });
}

function generateComponent(
  pascalName: string,
  kebabName: string,
  props: PropDef[],
): string {
  const propsInterface = props.length
    ? props
        .map(
          (p) =>
            `  /** ${p.name} */\n  ${p.name}${p.optional ? "?" : ""}: ${p.type};`,
        )
        .join("\n")
    : "  // Add props here";

  return `export interface ${pascalName}Props {
${propsInterface}
}

export function ${pascalName}({ ${props.map((p) => p.name).join(", ")} }: ${pascalName}Props) {
  return (
    <div className="${kebabName}">
      {/* Component content */}
    </div>
  );
}
`;
}

async function updateCategoryIndex(
  categoryDir: string,
  componentName: string,
): Promise<void> {
  const indexPath = join(categoryDir, "index.ts");

  if (!existsSync(indexPath)) {
    await writeFile(
      indexPath,
      `export * from "./${componentName}/index.js";\n`,
    );
    return;
  }

  const content = await readFile(indexPath, "utf-8");
  const exportLine = `export * from "./${componentName}/index.js";`;

  if (!content.includes(exportLine)) {
    await writeFile(indexPath, content + exportLine + "\n");
  }
}

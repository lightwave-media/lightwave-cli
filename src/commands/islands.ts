import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { mkdir, writeFile, readFile, readdir } from "fs/promises";
import { existsSync } from "fs";
import { join, basename } from "path";
import { getPackagePath } from "../utils/paths.js";

export const islandsCommand = new Command("islands")
  .alias("is")
  .description("Config-driven island component management");

const ISLAND_TYPES = ["hero", "menu", "footer", "section", "cta", "features"] as const;
type IslandType = (typeof ISLAND_TYPES)[number];

/**
 * lw islands:variants <type>
 * List available variants for an island type
 */
islandsCommand
  .command("variants <type>")
  .description("List available variants (e.g., lw islands:variants hero)")
  .action(async (type: string) => {
    const uiPath = getPackagePath("lightwave-ui");
    const registryPath = join(uiPath, "src/islands", `${type}-registry.tsx`);

    if (!existsSync(registryPath)) {
      console.log(chalk.yellow(`No registry found for: ${type}`));
      console.log(chalk.gray(`Expected: ${registryPath}`));
      console.log(chalk.gray(`\nAvailable types: ${ISLAND_TYPES.join(", ")}`));
      return;
    }

    const content = await readFile(registryPath, "utf-8");

    // Extract variants array
    const variantsMatch = content.match(/const \w+Variants = \[([\s\S]*?)\] as const/);
    if (!variantsMatch) {
      console.log(chalk.red("Could not parse variants from registry"));
      return;
    }

    const variants = variantsMatch[1]
      .match(/"([^"]+)"/g)
      ?.map((v) => v.replace(/"/g, "")) || [];

    console.log(chalk.blue(`\n=== ${type} Variants ===\n`));
    variants.forEach((v) => console.log(chalk.gray(`  ${v}`)));
    console.log(chalk.gray(`\nTotal: ${variants.length} variants`));
  });

/**
 * lw islands:add-variant <type> <variant>
 * Add a new variant to a registry
 */
islandsCommand
  .command("add-variant <type> <variant>")
  .description("Add a variant to registry (e.g., lw islands:add-variant hero split-image-01)")
  .option("--dry-run", "Preview changes without writing")
  .action(async (type: string, variant: string, options) => {
    const uiPath = getPackagePath("lightwave-ui");
    const registryPath = join(uiPath, "src/islands", `${type}-registry.tsx`);

    // Determine component path based on type
    const componentDirs: Record<string, string> = {
      hero: "marketing/header-section",
      menu: "marketing/header",
      footer: "marketing/footer",
      section: "marketing/sections",
      cta: "marketing/cta",
      features: "marketing/features",
    };

    const componentDir = componentDirs[type] || `marketing/${type}`;
    const componentPath = join(uiPath, "src/components", componentDir, `${type}-${variant}.tsx`);

    console.log(chalk.blue(`\n=== Add Variant: ${type}/${variant} ===\n`));

    // Check if component exists
    if (!existsSync(componentPath)) {
      console.log(chalk.red(`Component not found: ${componentPath}`));
      console.log(chalk.yellow("\nRun this first:"));
      console.log(chalk.gray(`  lw islands:scaffold ${type} ${variant}`));
      return;
    }

    // Check if component has headerProps support
    const componentContent = await readFile(componentPath, "utf-8");
    const hasHeaderProps = componentContent.includes("headerProps");

    if (!hasHeaderProps) {
      console.log(chalk.yellow("⚠ Component missing headerProps support"));
      console.log(chalk.gray("  Add to interface: headerProps?: HeaderProps;"));
      console.log(chalk.gray("  Add to destructuring: headerProps,"));
      console.log(chalk.gray("  Spread to Header: <Header {...headerProps} />"));

      if (!options.dryRun) {
        console.log(chalk.yellow("\nContinuing anyway... (component may need manual update)"));
      }
    } else {
      console.log(chalk.green("✓ Component has headerProps support"));
    }

    // Check if registry exists
    if (!existsSync(registryPath)) {
      console.log(chalk.yellow(`Registry not found. Creating: ${registryPath}`));
      if (!options.dryRun) {
        await writeFile(registryPath, generateRegistry(type, variant));
        console.log(chalk.green(`✓ Created registry: ${registryPath}`));
      }
      return;
    }

    // Update existing registry
    let registryContent = await readFile(registryPath, "utf-8");

    // Check if variant already exists
    if (registryContent.includes(`"${variant}"`)) {
      console.log(chalk.yellow(`Variant already in registry: ${variant}`));
      return;
    }

    // Generate import name
    const pascalName = kebabToPascal(`${type}-${variant}`);

    // Add import
    const importStatement = `import { ${pascalName} } from "@/components/${componentDir}/${type}-${variant}.js";`;

    // Find last import and add after
    const importMatch = registryContent.match(/import .* from .*;\n(?=\n|export)/);
    if (importMatch) {
      registryContent = registryContent.replace(
        importMatch[0],
        importMatch[0] + importStatement + "\n"
      );
    }

    // Add to variants array
    const variantsRegex = /const \w+Variants = \[([\s\S]*?)\] as const/;
    registryContent = registryContent.replace(variantsRegex, (match, inner) => {
      const trimmed = inner.trimEnd();
      const needsComma = trimmed && !trimmed.endsWith(",");
      return match.replace(inner, `${inner}${needsComma ? "," : ""}\n  "${variant}",`);
    });

    // Add to registry object
    const registryObjRegex = /const \w+Registry[^{]*\{([\s\S]*?)\n\};/;
    registryContent = registryContent.replace(registryObjRegex, (match, inner) => {
      const trimmed = inner.trimEnd();
      const needsComma = trimmed && !trimmed.endsWith(",");
      return match.replace(inner, `${inner}${needsComma ? "," : ""}\n  "${variant}": ${pascalName},`);
    });

    if (options.dryRun) {
      console.log(chalk.yellow("\n=== Dry Run: Registry Changes ===\n"));
      console.log(chalk.gray("Would add import:"));
      console.log(`  ${importStatement}`);
      console.log(chalk.gray("\nWould add to variants array:"));
      console.log(`  "${variant}"`);
      console.log(chalk.gray("\nWould add to registry:"));
      console.log(`  "${variant}": ${pascalName}`);
      return;
    }

    await writeFile(registryPath, registryContent);
    console.log(chalk.green(`✓ Added ${variant} to ${type}-registry.tsx`));
  });

/**
 * lw islands:scaffold <type> <name>
 * Scaffold a new island component with headerProps support
 */
islandsCommand
  .command("scaffold <type> <name>")
  .description("Scaffold a new island component (e.g., lw islands:scaffold hero my-custom)")
  .option("--dry-run", "Preview what would be created")
  .action(async (type: string, name: string, options) => {
    const uiPath = getPackagePath("lightwave-ui");

    const componentDirs: Record<string, string> = {
      hero: "marketing/header-section",
      menu: "marketing/header",
      footer: "marketing/footer",
      section: "marketing/sections",
      cta: "marketing/cta",
      features: "marketing/features",
    };

    const componentDir = componentDirs[type] || `marketing/${type}`;
    const componentName = `${type}-${name}`;
    const componentPath = join(uiPath, "src/components", componentDir, `${componentName}.tsx`);
    const pascalName = kebabToPascal(componentName);

    const componentContent = generateIslandComponent(type, name, pascalName);

    if (options.dryRun) {
      console.log(chalk.blue("\n=== Dry Run: Scaffold Island ===\n"));
      console.log(chalk.yellow("Would create:"), componentPath);
      console.log(chalk.yellow("\nGenerated code:\n"));
      console.log(componentContent);
      return;
    }

    if (existsSync(componentPath)) {
      console.log(chalk.red(`Component already exists: ${componentPath}`));
      return;
    }

    const spinner = ora(`Creating island: ${componentName}`).start();

    try {
      await mkdir(join(uiPath, "src/components", componentDir), { recursive: true });
      await writeFile(componentPath, componentContent);
      spinner.succeed(`Created island: ${componentName}`);
      console.log(chalk.gray(`  → ${componentPath}`));
      console.log(chalk.yellow("\nNext steps:"));
      console.log(chalk.gray(`  1. Customize the component`));
      console.log(chalk.gray(`  2. Run: lw islands:add-variant ${type} ${name}`));
    } catch (err) {
      spinner.fail(`Failed: ${err}`);
    }
  });

/**
 * lw islands:validate <config-file>
 * Validate a YAML config against registries
 */
islandsCommand
  .command("validate <config>")
  .description("Validate config against registries (e.g., lw islands:validate lightwave-config.yaml)")
  .action(async (configPath: string) => {
    const uiPath = getPackagePath("lightwave-ui");

    if (!existsSync(configPath)) {
      console.log(chalk.red(`Config not found: ${configPath}`));
      return;
    }

    // Parse YAML (simplified - just look for variant: lines)
    const configContent = await readFile(configPath, "utf-8");
    const variantMatches = configContent.matchAll(/variant:\s*["']?([^"'\n]+)["']?/g);

    console.log(chalk.blue("\n=== Config Validation ===\n"));

    const issues: string[] = [];
    const checked: string[] = [];

    for (const match of variantMatches) {
      const variant = match[1].trim();
      checked.push(variant);

      // Try to find which registry this belongs to
      let found = false;
      for (const type of ISLAND_TYPES) {
        const registryPath = join(uiPath, "src/islands", `${type}-registry.tsx`);
        if (existsSync(registryPath)) {
          const content = await readFile(registryPath, "utf-8");
          if (content.includes(`"${variant}"`)) {
            console.log(chalk.green(`  ✓ ${variant} (${type})`));
            found = true;
            break;
          }
        }
      }

      if (!found) {
        console.log(chalk.red(`  ✗ ${variant} (not in any registry)`));
        issues.push(variant);
      }
    }

    console.log(chalk.blue("\n=== Summary ==="));
    console.log(chalk.gray(`  Checked: ${checked.length} variants`));

    if (issues.length > 0) {
      console.log(chalk.red(`  Missing: ${issues.length}`));
      console.log(chalk.yellow("\n  Missing variants:"));
      issues.forEach((v) => console.log(chalk.gray(`    - ${v}`)));
    } else {
      console.log(chalk.green("  All variants valid!"));
    }
  });

/**
 * lw islands:check <type> <variant>
 * Check if a component has headerProps support
 */
islandsCommand
  .command("check <type> <variant>")
  .description("Check headerProps support (e.g., lw islands:check hero simple-text-01)")
  .option("--fix", "Add headerProps support if missing")
  .action(async (type: string, variant: string, options) => {
    const uiPath = getPackagePath("lightwave-ui");

    const componentDirs: Record<string, string> = {
      hero: "marketing/header-section",
      menu: "marketing/header",
      footer: "marketing/footer",
      section: "marketing/sections",
      cta: "marketing/cta",
      features: "marketing/features",
    };

    const componentDir = componentDirs[type] || `marketing/${type}`;
    const componentPath = join(uiPath, "src/components", componentDir, `${type}-${variant}.tsx`);

    if (!existsSync(componentPath)) {
      console.log(chalk.red(`Component not found: ${componentPath}`));
      return;
    }

    const content = await readFile(componentPath, "utf-8");

    console.log(chalk.blue(`\n=== Check: ${type}-${variant} ===\n`));

    const checks = [
      {
        name: "HeaderProps import",
        pattern: /import.*HeaderProps.*from/,
        fix: 'import type { HeaderProps } from "@/components/ui/header";',
      },
      {
        name: "headerProps in interface",
        pattern: /headerProps\??\s*:\s*HeaderProps/,
        fix: "  headerProps?: HeaderProps;",
      },
      {
        name: "headerProps destructuring",
        pattern: /\{\s*[^}]*headerProps[^}]*\}/,
        fix: "Add headerProps to destructuring",
      },
      {
        name: "Header spread",
        pattern: /<Header\s+\{\.\.\.headerProps\}/,
        fix: "<Header {...headerProps} />",
      },
    ];

    let allPassed = true;

    for (const check of checks) {
      const passed = check.pattern.test(content);
      if (passed) {
        console.log(chalk.green(`  ✓ ${check.name}`));
      } else {
        console.log(chalk.red(`  ✗ ${check.name}`));
        console.log(chalk.gray(`    Fix: ${check.fix}`));
        allPassed = false;
      }
    }

    if (allPassed) {
      console.log(chalk.green("\n✓ Component is registry-ready!"));
    } else {
      console.log(chalk.yellow("\n⚠ Component needs updates for registry"));
    }
  });

/**
 * lw islands:list
 * List all registries and their variants
 */
islandsCommand
  .command("list")
  .description("List all island registries")
  .action(async () => {
    const uiPath = getPackagePath("lightwave-ui");
    const islandsDir = join(uiPath, "src/islands");

    console.log(chalk.blue("\n=== Island Registries ===\n"));

    if (!existsSync(islandsDir)) {
      console.log(chalk.gray("No islands directory found"));
      console.log(chalk.gray(`Expected: ${islandsDir}`));
      return;
    }

    const files = await readdir(islandsDir);
    const registries = files.filter((f) => f.endsWith("-registry.tsx"));

    if (registries.length === 0) {
      console.log(chalk.gray("No registries found"));
      return;
    }

    for (const registry of registries) {
      const type = registry.replace("-registry.tsx", "");
      const content = await readFile(join(islandsDir, registry), "utf-8");

      const variantsMatch = content.match(/const \w+Variants = \[([\s\S]*?)\] as const/);
      const variants = variantsMatch?.[1]
        .match(/"([^"]+)"/g)
        ?.map((v) => v.replace(/"/g, "")) || [];

      console.log(chalk.yellow(`${type}/`) + chalk.gray(` (${variants.length} variants)`));
      variants.forEach((v) => console.log(chalk.gray(`  └─ ${v}`)));
    }
  });

/**
 * lw islands:init <type>
 * Initialize a new registry for an island type
 */
islandsCommand
  .command("init <type>")
  .description("Initialize a new registry (e.g., lw islands:init testimonials)")
  .option("--dry-run", "Preview what would be created")
  .action(async (type: string, options) => {
    const uiPath = getPackagePath("lightwave-ui");
    const islandsDir = join(uiPath, "src/islands");
    const registryPath = join(islandsDir, `${type}-registry.tsx`);

    if (existsSync(registryPath)) {
      console.log(chalk.yellow(`Registry already exists: ${registryPath}`));
      return;
    }

    const registryContent = generateEmptyRegistry(type);

    if (options.dryRun) {
      console.log(chalk.blue("\n=== Dry Run: Init Registry ===\n"));
      console.log(chalk.yellow("Would create:"), registryPath);
      console.log(chalk.yellow("\nGenerated code:\n"));
      console.log(registryContent);
      return;
    }

    const spinner = ora(`Creating registry: ${type}-registry.tsx`).start();

    try {
      await mkdir(islandsDir, { recursive: true });
      await writeFile(registryPath, registryContent);
      spinner.succeed(`Created registry: ${type}-registry.tsx`);
      console.log(chalk.gray(`  → ${registryPath}`));
      console.log(chalk.yellow("\nNext:"));
      console.log(chalk.gray(`  lw islands:scaffold ${type} my-variant`));
      console.log(chalk.gray(`  lw islands:add-variant ${type} my-variant`));
    } catch (err) {
      spinner.fail(`Failed: ${err}`);
    }
  });

// Helper functions

function kebabToPascal(kebab: string): string {
  return kebab
    .split("-")
    .map((s) => s.charAt(0).toUpperCase() + s.slice(1))
    .join("");
}

function generateIslandComponent(type: string, name: string, pascalName: string): string {
  return `import type { HeaderProps } from "@/components/ui/header";
import { Header } from "@/components/ui/header";

export interface ${pascalName}Props {
  /** Header configuration - required for island components */
  headerProps?: HeaderProps;
  /** Main headline */
  headline?: string;
  /** Description text */
  description?: string;
  /** Primary CTA text */
  primaryCtaText?: string;
  /** Primary CTA href */
  primaryCtaHref?: string;
  /** Secondary CTA text */
  secondaryCtaText?: string;
  /** Secondary CTA href */
  secondaryCtaHref?: string;
}

export function ${pascalName}({
  headerProps,
  headline = "Your Headline Here",
  description = "Your description here.",
  primaryCtaText = "Get Started",
  primaryCtaHref = "#",
  secondaryCtaText,
  secondaryCtaHref,
}: ${pascalName}Props) {
  return (
    <div className="relative">
      {/* Header with nav - receives config from Django */}
      <Header {...headerProps} />

      {/* ${type} content */}
      <section className="relative py-20 lg:py-32">
        <div className="container mx-auto px-4">
          <div className="max-w-3xl mx-auto text-center">
            <h1 className="text-4xl md:text-5xl lg:text-6xl font-bold tracking-tight">
              {headline}
            </h1>
            <p className="mt-6 text-lg text-muted-foreground">
              {description}
            </p>
            <div className="mt-10 flex flex-wrap justify-center gap-4">
              <a
                href={primaryCtaHref}
                className="inline-flex items-center justify-center rounded-md bg-primary px-6 py-3 text-sm font-medium text-primary-foreground shadow hover:bg-primary/90"
              >
                {primaryCtaText}
              </a>
              {secondaryCtaText && (
                <a
                  href={secondaryCtaHref || "#"}
                  className="inline-flex items-center justify-center rounded-md border border-input bg-background px-6 py-3 text-sm font-medium shadow-sm hover:bg-accent hover:text-accent-foreground"
                >
                  {secondaryCtaText}
                </a>
              )}
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}

export default ${pascalName};
`;
}

function generateRegistry(type: string, variant: string): string {
  const pascal = kebabToPascal(type);
  const variantPascal = kebabToPascal(`${type}-${variant}`);

  return `import type { ComponentType } from "react";
import type { HeaderProps } from "@/components/ui/header";
import { ${variantPascal} } from "@/components/marketing/${type}/${type}-${variant}.js";

// 1. Base props interface
export interface Base${pascal}Props {
  headerProps?: HeaderProps;
}

// 2. Variant keys
export const ${type}Variants = [
  "${variant}",
] as const;

export type ${pascal}Variant = (typeof ${type}Variants)[number];

// 3. Type guard
export function is${pascal}Variant(value: string): value is ${pascal}Variant {
  return ${type}Variants.includes(value as ${pascal}Variant);
}

// 4. Registry mapping
export const ${type}Registry: Record<
  ${pascal}Variant,
  ComponentType<Base${pascal}Props & Record<string, unknown>>
> = {
  "${variant}": ${variantPascal},
};

// 5. Getter function
export function get${pascal}Component(variant: string) {
  if (!is${pascal}Variant(variant)) {
    console.warn(\`[${pascal}Registry] Unknown variant: \${variant}\`);
    return null;
  }
  return ${type}Registry[variant];
}

// 6. Default variant
export const DEFAULT_${type.toUpperCase()}_VARIANT: ${pascal}Variant = "${variant}";
`;
}

function generateEmptyRegistry(type: string): string {
  const pascal = kebabToPascal(type);

  return `import type { ComponentType } from "react";
import type { HeaderProps } from "@/components/ui/header";

// 1. Base props interface
export interface Base${pascal}Props {
  headerProps?: HeaderProps;
}

// 2. Variant keys
export const ${type}Variants = [
  // Add variants here
] as const;

export type ${pascal}Variant = (typeof ${type}Variants)[number];

// 3. Type guard
export function is${pascal}Variant(value: string): value is ${pascal}Variant {
  return ${type}Variants.includes(value as ${pascal}Variant);
}

// 4. Registry mapping
export const ${type}Registry: Record<
  ${pascal}Variant,
  ComponentType<Base${pascal}Props & Record<string, unknown>>
> = {
  // Add variant mappings here
};

// 5. Getter function
export function get${pascal}Component(variant: string) {
  if (!is${pascal}Variant(variant)) {
    console.warn(\`[${pascal}Registry] Unknown variant: \${variant}\`);
    return null;
  }
  return ${type}Registry[variant];
}

// 6. Default variant (update after adding first variant)
// export const DEFAULT_${type.toUpperCase()}_VARIANT: ${pascal}Variant = "your-first-variant";
`;
}

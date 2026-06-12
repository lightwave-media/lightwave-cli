package zodgen

import (
	"fmt"
	"reflect"
)

// CheckPropFieldParity asserts that component_contract and section_contract
// declare field-for-field identical PropField sub_schemas. The stamps mirror
// the shape deliberately (SST sub_schemas are file-local; no cross-file
// $ref), and lightwave-core's data/ui/__index.yaml records this assertion as
// the enforcement mechanism — generation must fail on drift, per
// lightwave-cli#77.
func CheckPropFieldParity(component, section *Schema) error {
	cp, ok := component.SubSchemas["PropField"]
	if !ok {
		return fmt.Errorf("component_contract (%s) has no PropField sub_schema", component.Meta.SchemaID)
	}

	sp, ok := section.SubSchemas["PropField"]
	if !ok {
		return fmt.Errorf("section_contract (%s) has no PropField sub_schema", section.Meta.SchemaID)
	}

	if !reflect.DeepEqual(cp, sp) {
		return fmt.Errorf(
			"PropField parity violated between %s and %s — the shapes must stay field-for-field identical (see data/ui/__index.yaml); fix the drifted side before generating",
			component.Meta.SchemaID, section.Meta.SchemaID,
		)
	}

	return nil
}

package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/loongson-community/loongarch-opcodes/scripts/go/common"
)

func main() {
	inputs := os.Args[1:]

	descs, err := common.ReadInsnDescs(inputs)
	if err != nil {
		panic(err)
	}

	formats := gatherFormats(descs)
	scs := gatherDistinctSlotCombinations(formats)

	sort.Slice(descs, func(i int, j int) bool {
		return descs[i].Word < descs[j].Word
	})

	sort.Slice(formats, func(i int, j int) bool {
		return formats[i].CanonicalRepr() < formats[j].CanonicalRepr()
	})

	var ectx common.EmitterCtx

	ectx.Emit("// Code generated by loongson-community/loongarch-opcodes geninsndata; DO NOT EDIT.\n\n")
	ectx.Emit("package loong\n\n")
	ectx.Emit("import \"cmd/internal/obj\"\n\n")

	emitInsnFormatTypes(&ectx, formats)

	for _, f := range formats {
		emitValidatorForFormat(&ectx, f)
	}

	emitValidatorMapping(&ectx, formats)
	emitSlotEncoders(&ectx, scs)
	emitBigEncoderFn(&ectx, formats)
	emitInsnEncodings(&ectx, descs)

	result := ectx.Finalize()
	os.Stdout.Write(result)
}

////////////////////////////////////////////////////////////////////////////

func gatherFormats(descs []*common.InsnDescription) []*common.InsnFormat {
	formatsSet := make(map[string]*common.InsnFormat)
	for _, d := range descs {
		canonicalFormatName := d.Format.CanonicalRepr()
		if _, ok := formatsSet[canonicalFormatName]; !ok {
			formatsSet[canonicalFormatName] = d.Format
		}
	}

	result := make([]*common.InsnFormat, 0, len(formatsSet))
	for _, f := range formatsSet {
		result = append(result, f)
	}

	return result
}

const (
	slotD = 0
	slotJ = 5
	slotK = 10
	slotA = 15
	slotM = 16
)

func gatherDistinctSlotCombinations(fmts []*common.InsnFormat) []string {
	slotCombinationsSet := make(map[string]struct{})
	for _, f := range fmts {
		// skip EMPTY
		if len(f.Args) == 0 {
			continue
		}
		slotCombinationsSet[slotCombinationForFmt(f)] = struct{}{}
	}

	result := make([]string, 0, len(slotCombinationsSet))
	for sc := range slotCombinationsSet {
		result = append(result, sc)
	}
	sort.Strings(result)

	return result
}

// slot combination looks like "DJKM"
func slotCombinationForFmt(f *common.InsnFormat) string {

	var slots []int
	for _, a := range f.Args {
		for _, s := range a.Slots {
			slots = append(slots, int(s.Offset))
		}
	}
	sort.Ints(slots)

	var sb strings.Builder
	for _, s := range slots {
		switch s {
		case slotD:
			sb.WriteRune('D')
		case slotJ:
			sb.WriteRune('J')
		case slotK:
			sb.WriteRune('K')
		case slotA:
			sb.WriteRune('A')
		case slotM:
			sb.WriteRune('M')
		default:
			panic("should never happen")
		}
	}

	return sb.String()
}

func slotOffsetFromRune(s rune) int {
	switch s {
	case 'D', 'd':
		return slotD
	case 'J', 'j':
		return slotJ
	case 'K', 'k':
		return slotK
	case 'A', 'a':
		return slotA
	case 'M', 'm':
		return slotM
	default:
		panic("should never happen")
	}
}

////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////

func emitInsnFormatTypes(ectx *common.EmitterCtx, fmts []*common.InsnFormat) {
	ectx.Emit("type insnFormat int\n\nconst (\n")
	ectx.Emit("\tinsnFormatUnknown insnFormat = iota\n")

	for _, f := range fmts {
		ectx.Emit("\tinsnFormat%s\n", f.CanonicalRepr())
	}

	ectx.Emit(")\n\n")

	emitInsnFormatArityFn(ectx, fmts)
}

func emitInsnFormatArityFn(
	ectx *common.EmitterCtx,
	fmts []*common.InsnFormat,
) {
	arityMap := make(map[int][]*common.InsnFormat)
	for _, f := range fmts {
		arity := len(f.Args)
		arityMap[arity] = append(arityMap[arity], f)
	}

	ectx.Emit("func (f insnFormat) arity() int {\n")
	ectx.Emit("\tswitch f {\n")
	for arity := 0; arity < 5; arity++ {
		cases := arityMap[arity]

		ectx.Emit("\tcase ")
		for i, f := range cases {
			sep := ","
			if i == len(cases)-1 {
				sep = ":"
			}
			ectx.Emit("insnFormat%s%s\n", f.CanonicalRepr(), sep)
		}
		ectx.Emit("\t\treturn %d\n", arity)
	}
	ectx.Emit("\t}\n\n\tpanic(\"unknown insn format\")\n")
	ectx.Emit("}\n\n")
}

func emitInsnEncodings(ectx *common.EmitterCtx, descs []*common.InsnDescription) {
	ectx.Emit("type encoding struct {\n")
	ectx.Emit("\tbits uint32\n")
	ectx.Emit("\tfmt  insnFormat\n")
	ectx.Emit("}\n\n")
	ectx.Emit("var encodings = [ALAST & obj.AMask]encoding{\n")

	for _, d := range descs {
		goOpcodeName := common.GoAnameForInsn(d.Mnemonic)
		formatName := "insnFormat" + d.Format.CanonicalRepr()

		ectx.Emit(
			"\t%s & obj.AMask: {bits: 0x%08x, fmt: %s},\n",
			goOpcodeName,
			d.Word,
			formatName,
		)
	}

	ectx.Emit("}\n")
}

func insnFieldNameForRegArg(a *common.Arg) string {
	switch a.Slots[0].Offset {
	case slotD:
		return "rd"
	case slotJ:
		return "rj"
	case slotK:
		return "rk"
	case slotA:
		return "ra"
	default:
		panic("should never happen")
	}
}

func fieldNamesForArgs(args []*common.Arg) []string {
	argFieldNames := make([]string, len(args))
	immIdx := 0
	for i, a := range args {
		if a.Kind.IsImm() {
			immIdx++
			argFieldNames[i] = fmt.Sprintf("imm%d", immIdx)
		} else {
			// register operand
			argFieldNames[i] = insnFieldNameForRegArg(a)
		}
	}
	return argFieldNames
}

func verifierFnNameForFormat(f *common.InsnFormat) string {
	return "validate" + f.CanonicalRepr()
}

func emitValidatorMapping(ectx *common.EmitterCtx, fmts []*common.InsnFormat) {
	ectx.Emit("var validators = [...]func(*instruction) error {\n")

	for _, f := range fmts {
		formatName := f.CanonicalRepr()
		verifierFnName := verifierFnNameForFormat(f)
		ectx.Emit("\tinsnFormat%s: %s,\n", formatName, verifierFnName)
	}

	ectx.Emit("\t}\n\n")
}

func emitValidatorForFormat(ectx *common.EmitterCtx, f *common.InsnFormat) {
	funcName := verifierFnNameForFormat(f)

	argFieldNames := fieldNamesForArgs(f.Args)

	ectx.Emit("func %s(insn *instruction) error {\n", funcName)

	// things to emit:
	//
	// for every arg X:
	//     if err := want<arg type>("argX", argX); err != nil {
	//         return err
	//     }
	for argIdx, a := range f.Args {
		argParamName := "insn." + argFieldNames[argIdx]

		ectx.Emit("\tif err := ")

		switch a.Kind {
		case common.ArgKindIntReg:
			ectx.Emit("wantIntReg(insn.as, %s)", argParamName)

		case common.ArgKindFPReg:
			ectx.Emit("wantFPReg(insn.as, %s)", argParamName)

		case common.ArgKindFCCReg:
			ectx.Emit("wantFCCReg(insn.as, %s)", argParamName)

		case common.ArgKindSignedImm,
			common.ArgKindUnsignedImm:
			// want[Un]signedImm(argX, width)
			var wantFuncName string
			if a.Kind == common.ArgKindSignedImm {
				wantFuncName = "wantSignedImm"
			} else {
				wantFuncName = "wantUnsignedImm"
			}

			ectx.Emit("%s(insn.as, %s, %d)", wantFuncName, argParamName, a.TotalWidth())
		}

		ectx.Emit("; err != nil {\n\t\treturn err\n\t}\n")
	}

	ectx.Emit("\treturn nil\n}\n\n")
}

func emitSlotEncoders(ectx *common.EmitterCtx, scs []string) {
	for _, sc := range scs {
		emitSlotEncoderFn(ectx, sc)
	}
}

func slotEncoderFnNameForSc(sc string) string {
	plural := ""
	if len(sc) > 1 {
		plural = "s"
	}

	return fmt.Sprintf("encode%sSlot%s", sc, plural)
}

func emitSlotEncoderFn(ectx *common.EmitterCtx, sc string) {
	funcName := slotEncoderFnNameForSc(sc)
	scLower := strings.ToLower(sc)

	ectx.Emit("func %s(bits uint32", funcName)
	for _, s := range scLower {
		ectx.Emit(", %c uint32", s)
	}
	ectx.Emit(") uint32 {\n")

	ectx.Emit("return bits")

	for _, s := range scLower {
		offset := slotOffsetFromRune(s)

		ectx.Emit(" | %c", s)
		if offset > 0 {
			ectx.Emit("<<%d", offset)
		}
	}

	ectx.Emit("\n}\n\n")
}

func emitBigEncoderFn(ectx *common.EmitterCtx, fmts []*common.InsnFormat) {
	ectx.Emit(`func (insn *instruction) encodeReal() (uint32, error) {
	enc, err := encodingForAs(insn.as)
	if err != nil {
		return 0, err
	}

	switch enc.fmt {
`)

	for _, f := range fmts {
		formatName := f.CanonicalRepr()
		ectx.Emit("\tcase insnFormat%s:\n", formatName)

		// special-case EMPTY
		if len(f.Args) == 0 {
			ectx.Emit("\t\treturn enc.bits, nil\n")
			continue
		}

		argFieldNames := fieldNamesForArgs(f.Args)

		argVarNames := make([]string, len(f.Args))
		for i, a := range f.Args {
			argVarNames[i] = strings.ToLower(a.CanonicalRepr())
		}

		for i, a := range f.Args {
			varName := argVarNames[i]
			fieldExpr := "insn." + argFieldNames[i]

			ectx.Emit("%s :=", varName)

			switch a.Kind {
			case common.ArgKindIntReg:
				ectx.Emit("regInt(%s)", fieldExpr)
			case common.ArgKindFPReg:
				ectx.Emit("regFP(%s)", fieldExpr)
			case common.ArgKindFCCReg:
				ectx.Emit("regFCC(%s)", fieldExpr)
			case common.ArgKindSignedImm, common.ArgKindUnsignedImm:
				widthMask := (1 << a.TotalWidth()) - 1
				ectx.Emit("uint32(%s) & 0x%x", fieldExpr, widthMask)
			default:
				panic("unreachable")
			}

			ectx.Emit("\n")
		}

		// collect slot expressions
		slotExprs := make(map[uint]string)
		for argIdx, a := range f.Args {
			argVarName := argVarNames[argIdx]

			if len(a.Slots) == 1 {
				slotExprs[a.Slots[0].Offset] = argVarName
			} else {
				// remainingBits is shift amount to extract the current slot from arg
				//
				// take example of Sd5k16:
				//
				// Sd5k16 = (MSB) DDDDDKKKKKKKKKKKKKKKK (LSB)
				//
				// initially remainingBits = 5+16
				//
				// consume from left to right:
				//
				// slot d5: remainingBits = 16
				// thus d5 = (sd5k16 >> 16) & 0b11111
				// emit (d5 expr above)
				//
				// slot k16: remainingBits = 0
				// thus k16 = (sd5k16 >> 0) & 0b1111111111111111
				//          = sd5k16 & 0b1111111111111111
				// emit (k16 expr above)
				remainingBits := int(a.TotalWidth())
				for _, s := range a.Slots {
					remainingBits -= int(s.Width)
					mask := int((1 << s.Width) - 1)

					var sb strings.Builder
					sb.WriteString(argVarName)

					if remainingBits > 0 {
						sb.WriteString(">>")
						sb.WriteString(strconv.Itoa(remainingBits))
					}

					sb.WriteString("&0x")
					sb.WriteString(strconv.FormatUint(uint64(mask), 16))

					slotExprs[s.Offset] = sb.String()
				}
			}
		}

		sc := slotCombinationForFmt(f)
		encFnName := slotEncoderFnNameForSc(sc)
		ectx.Emit("return %s(enc.bits", encFnName)

		for _, s := range sc {
			offset := uint(slotOffsetFromRune(s))
			slotExpr, ok := slotExprs[offset]
			if !ok {
				panic("should never happen")
			}
			ectx.Emit(", %s", slotExpr)
		}

		ectx.Emit("), nil\n")
	}

	ectx.Emit("\tdefault:\n\t\tpanic(\"should never happen: unknown format for real insn\")\n")
	ectx.Emit("\t}\n}\n")
}

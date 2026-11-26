package ocr

import ()

// Endpoint paths for the OCR family.
//
// They are kept as constants rather than generated code so that a path change
// is a one-line diff with an obvious blast radius.
const (
	pathResult = "/vision/v1/result"

	pathParseGeneral     = "/vision/v1/document/parse/general/async"
	pathParseHunyuan     = "/vision/v1/document/parse/hunyuan-ocr/async"
	pathParseMultilang   = "/vision/v1/document/parse/doc-multilang/async"
	pathParseArabic      = "/vision/v1/document/parse/ar-doc/async"
	pathOCRGeneral       = "/vision/v1/ocr/general/async"
	pathOCRPaddle        = "/vision/v1/ocr/paddle/async"
	pathOCRSeal          = "/vision/v1/ocr/general/seals/async"
	pathOCRPageDirection = "/vision/v1/ocr/page-direction/async"
	pathBankStatement    = "/vision/v1/receipt/bank-statement/async"
	pathMedicalReceipt   = "/vision/v1/receipt/medical/async"
	pathFinancialReceipt = "/vision/v1/receipt/financial/async"
	pathImageBlur        = "/vision/v1/ai-detection/image-blur/async"

	pathTTSSpeech = "/tts/v1/speech"
)

// CardKind selects one of the card recognition variants.
//
// All card variants share the same request shape (a single file) and differ only
// by path, so this is a type selector rather than thirteen separate methods.
type CardKind string

// Supported card kinds.
const (
	CardGeneric       CardKind = "recognize"
	CardIDCard        CardKind = "idcard"
	CardBankCard      CardKind = "bankcard"
	CardResidenceBook CardKind = "residencebook"
	CardVehicleLic    CardKind = "vehiclelic"
	CardDriverLic     CardKind = "driverlic"
	CardCompany       CardKind = "company"
	CardDegree        CardKind = "degree"
	CardBirth         CardKind = "birth"
	CardHKTaiwan      CardKind = "hktaiwancard"
	CardPassport      CardKind = "passport"
	CardMilitary      CardKind = "military"
	CardMarriage      CardKind = "marriage"
)

// cardKinds is the canonical ordering used by CardKinds.
var cardKinds = []CardKind{
	CardGeneric,
	CardIDCard,
	CardBankCard,
	CardResidenceBook,
	CardVehicleLic,
	CardDriverLic,
	CardCompany,
	CardDegree,
	CardBirth,
	CardHKTaiwan,
	CardPassport,
	CardMilitary,
	CardMarriage,
}

// CardKinds returns every supported card kind, in a stable order.
func CardKinds() []CardKind {
	out := make([]CardKind, len(cardKinds))
	copy(out, cardKinds)
	return out
}

// valid reports whether k is a known card kind.
func (k CardKind) valid() bool {
	for _, known := range cardKinds {
		if k == known {
			return true
		}
	}
	return false
}

// path returns the submission path for the card kind and mode.
func (k CardKind) path(sync bool) string {
	suffix := "/async"
	if sync {
		suffix = "/sync"
	}
	return "/vision/v1/card/" + string(k) + suffix
}

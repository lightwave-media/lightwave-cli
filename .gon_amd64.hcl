# gon config for the darwin/amd64 build of `lw`.
#
# Activated by .github/workflows/sign-macos.yml when called from
# release.yml. Inert until those secrets exist and the call is
# uncommented — see docs/release-notarization.md for the activation
# steps.
#
# Bundle ID: io.lightwave-media.cli — matches the homebrew tap
# `Formula/lw.rb`'s `name "lw"`. If you change this, also change it
# in .gon_arm64.hcl and (if relevant) the formula's name/description.

source = ["./dist/lw_darwin_amd64_v1/lw"]
bundle_id = "io.lightwave-media.cli"

apple_id {
  username = "@env:AC_USERNAME"
  password = "@env:AC_PASSWORD"
  provider = "@env:AC_PROVIDER"
}

sign {
  application_identity = "Developer ID Application: LightWave Media, LLC"
}

zip {
  output_path = "./dist/lw_darwin_amd64_signed.zip"
}

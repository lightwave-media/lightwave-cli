# gon config for the darwin/arm64 build of `lw`.
# See .gon_amd64.hcl for activation context.

source = ["./dist/lw_darwin_arm64_v8.0/lw"]
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
  output_path = "./dist/lw_darwin_arm64_signed.zip"
}

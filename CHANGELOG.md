# Changelog

## v2.19.0 (development)

- In an interactive TTY, zero-argument `amq-squad run start` now opens the
  guided wizard instead of returning the historical missing-flag error.
  Non-TTY and CI invocations remain noninteractive and retain that error.
- The wizard executes the canonical preview first and then asks
  `Launch now? [y/N]`; No is the default, and explicit Yes reruns the identical
  argv with only `--go` appended. It also offers a Global/NOC scope backed by
  canonical `amq-squad global start`.

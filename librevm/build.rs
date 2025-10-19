use std::env;

fn main() {
    let crate_dir = env::var("CARGO_MANIFEST_DIR").unwrap();

    cbindgen::generate(crate_dir).expect("Unable to generate bindings").write_to_file("bindings.h");

    prost_build
        ::compile_protos(
            &[
                "../proto/cosmos/evm/vm/v1/internal/internal.proto",
                "../proto/cosmos/evm/vm/v1/internal/block.proto",
                "../proto/cosmos/evm/vm/v1/internal/result.proto",
                "../proto/cosmos/evm/vm/v1/internal/state.proto",
            ],
            &["../proto/cosmos/evm/vm/v1/internal/"]
        )
        .expect("Failed to compile protos");
}

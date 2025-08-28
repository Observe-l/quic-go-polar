# FEC Performance Evaluation Plan — Polar vs. RLC vs. RS

## 1. Objective
Evaluate and compare the **decoding success rate, runtime performance, and estimated energy consumption** of three packet-level FEC schemes (Polar code, Random Linear Coding, Reed-Solomon) under controlled simulation.

---

## 2. Test Configurations

- **FEC Code Rates (N,K):**
  - (8,7), (8,6), (16,12), (16,11), (32,26)
- **Packet Size:** fixed at 1500 bytes (MTU).  
- **Source Data per Simulation:** 2 MB random data.  
- **Simulation Runs:** 10,0000 independent repetitions per configuration and per packet loss rate.  
- **Packet Loss Rates:** {0.5%, 1%, 3%, 5%}.  
- **Platform Assumption:** ARM Cortex-A7 class CPU (low-power embedded device).

---

## 3. Simulation Workflow

### Step 1: Data Preparation
- Generate 2 MB random source data.  
- Segment into `K` source packets of 1500 bytes.  

### Step 2: Encoding
- **Polar Code:**  
  - Use **pre-computed tables** (lookup for Information Set `A` and parity ordering).  
  - Fixed tables used:  
    - N8_K7_e1  
    - N8_K6_e1  
    - N16_K12_e1  
    - N16_K11_e3  
    - N32_K26_e3  
- **RLC:** generate random GF(2^8) coefficient vectors; multiply source packets.  
- **RS:** encode over GF(2^8) with generator polynomial.  

### Step 3: Transmission
- Concatenate encoded packets.  
- Randomly drop packets according to the loss rate (i.i.d.).  

### Step 4: Decoding
- **Polar Code:** Gaussian elimination (GF(2) XOR ops only) guided by lookup table.  
- **RLC:** Gaussian elimination (GF(2^8) multiply/add).  
- **RS:** Berlekamp–Massey / Forney decoding (GF(2^8) multiply/add).  

### Step 5: Success Evaluation
- Mark success if all source data fully reconstructed.  
- Only count transmission attempts that succeed in recovery.  

### Step 6: Repeat
- Repeat the entire encode–transmit–decode cycle **10,000 times** for each (N,K) and each loss rate.  

---

## 4. Metrics to Record

For each scheme, each (N,K), and each loss rate:

- **Success Rate (%):**  
  Fraction of 10,0000 runs with successful full recovery.  

- **Encoding Time (ms):**  
  - Total encoding time (sum over 10,0000 runs).  
  - Average encoding time per run.  

- **Decoding Time (ms):**  
  - Total decoding time (sum over 10,0000 runs).  
  - Average decoding time per run.  

- **Energy Estimate:**  
  Based on ARM-A7 instruction cost model:  
  - XOR/bitwise op ~ 1 cycle,  
  - Addition ~ 1–2 cycles,  
  - Multiplication (GF(2^8)) ~ 3–5 cycles.  
  Approximate energy as proportional to total instruction counts:  
  $$
  E \propto N_{\text{xor}} + 2\cdot N_{\text{add}} + 5\cdot N_{\text{mul}}
  $$

---

## 5. Expected Output

### 5.1 Success Rate (%)

| Scheme | (N,K) | Loss=0.5% | Loss=1% | Loss=3% | Loss=5% |
|--------|-------|-----------|---------|---------|---------|
| Polar  | 8,7   | ____      | ____    | ____    | ____    |
| Polar  | 8,6   | ____      | ____    | ____    | ____    |
| RLC    | 8,7   | ____      | ____    | ____    | ____    |
| RS     | 8,7   | ____      | ____    | ____    | ____    |
| ...    | ...   | ...       | ...     | ...     | ...     |

### 5.2 Encoding Time (ms)

| Scheme | (N,K) | Total Time (10k runs) | Avg per Run |
|--------|-------|------------------------|-------------|
| Polar  | 8,7   | ____                  | ____        |
| Polar  | 8,6   | ____                  | ____        |
| RLC    | 8,7   | ____                  | ____        |
| RS     | 8,7   | ____                  | ____        |
| ...    | ...   | ...                   | ...         |

### 5.3 Decoding Time (ms)

| Scheme | (N,K) | Total Time (10k runs) | Avg per Run |
|--------|-------|------------------------|-------------|
| Polar  | 8,7   | ____                  | ____        |
| RLC    | 8,7   | ____                  | ____        |
| RS     | 8,7   | ____                  | ____        |
| ...    | ...   | ...                   | ...         |

### 5.4 Energy Estimate (relative units)

| Scheme | (N,K) | Loss=0.5% | Loss=1% | Loss=3% | Loss=5% |
|--------|-------|-----------|---------|---------|---------|
| Polar  | 8,7   | ____      | ____    | ____    | ____    |
| RLC    | 8,7   | ____      | ____    | ____    | ____    |
| RS     | 8,7   | ____      | ____    | ____    | ____    |
| ...    | ...   | ...       | ...     | ...     |

---

## 6. Deliverables
1. Completed **Markdown report file** containing:
   - Success rates, times, energy estimates in tables.  
   - Analysis comparing Polar vs. RLC vs. RS.  
2. Simulation logs (optional) for reproducibility.  

---

## 7. Acceptance Criteria
- All metrics averaged over 10,000 runs.  
- Success rate differences across methods are visible and interpretable.  
- Timing results consistent with complexity expectations:  
  - Polar < RLC < RS in energy cost (since Polar uses only XOR).  
  - RS and RLC both show GF(2^8) multiplication overhead.  
- Energy estimates qualitatively match cycle-level predictions for ARM-A7.

---

**Document Version:** v1.0 — Performance Evaluation Plan
